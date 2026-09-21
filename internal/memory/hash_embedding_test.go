package memory

import (
	"testing"
)

// TestTokenize_CoversCJKAndSeparatesPunctuation pins the tokenizer the hash
// embedder depends on.
//
// It used to test `r > 0x4e00`, which is wrong in both directions. U+4E00 is 一,
// so `>` excluded it — "一二三" tokenised to just ["二三"], and a document
// containing 一 lost the character entirely. And every rune above the threshold
// counted as a word character, so fullwidth punctuation was glued into tokens:
// "写，注意先切流量" came out as one word.
func TestTokenize_CoversCJKAndSeparatesPunctuation(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"一二三", []string{"一二三"}},
		{"部署脚本要用 Go 写，注意先切流量", []string{"部署脚本要用", "Go", "写", "注意先切流量"}},
		{"deployment script, please", []string{"deployment", "script", "please"}},
		{"订单A-1001已发货。", []string{"订单A", "1001已发货"}},
	}
	for _, c := range cases {
		got := tokenize(c.in)
		if len(got) != len(c.want) {
			t.Errorf("tokenize(%q) = %q, want %q", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("tokenize(%q) = %q, want %q", c.in, got, c.want)
				break
			}
		}
	}
}

// hashFixture is the pair set the dimension and cut-off were measured against:
// chunk-sized passages (a document chunk is ~400 runes) where the relevant pair
// really does share terms. A pair sharing nothing measures nothing, because the
// embedder is purely lexical.
type hashFixture struct {
	name  string
	query string
	rel   string
	unrel string
}

func hashFixtures() []hashFixture {
	zhRel := "发布流程说明：先确认备份已经完成，然后切流量到新版本，观察监控指标十分钟。如果指标正常就保持新版本，如果出现异常则立即回退旧版本。回退之后要核对数据一致性，并记录本次发布的原因与结果。" +
		"补充：灰度发布时先放百分之一的流量，确认无异常后再逐步放大到全量。"
	zhUnrel := "食堂菜单说明：周一供应拉面，周二供应盖饭，周三供应饺子，周四供应炒面，周五供应火锅。每份套餐都包含一份小菜和一碗汤。" +
		"早餐供应时间为七点到九点，午餐为十一点半到一点，晚餐为五点半到七点。节假日照常供应，但菜单可能调整。"
	zhThird := "员工报销流程：先在系统里提交报销单，附上发票照片，由直属主管审批。审批通过后财务在三个工作日内打款。" +
		"差旅报销需要额外提供行程单。超过五千元的报销需要部门负责人二次审批。"
	enRel := "Release process: confirm the backup finished, then shift traffic to the new version and watch the metrics for ten minutes. " +
		"If the metrics look normal, keep the new version; if anything looks wrong, roll back to the old version immediately. After a rollback, verify data consistency and record why the release happened."
	enUnrel := "Canteen menu: ramen on Monday, rice bowls on Tuesday, dumplings on Wednesday, fried noodles on Thursday and hot pot on Friday. " +
		"Every set meal includes a side dish and a bowl of soup. Breakfast runs from seven to nine, lunch from half past eleven to one."

	// Short passages too: these are the pairs that inverted hardest at the
	// original dimension (relevant 0.033 against unrelated 0.311), so the ranking
	// guard fails there rather than leaning on the occupancy check alone.
	zhShortRel := "发布流程：先确认备份完成，然后切流量到新版本，观察监控指标十分钟，确认无误后回退旧版本。"
	zhShortUnrel := "食堂菜单：周一拉面，周二盖饭，周三饺子，周四炒面，周五火锅。"

	return []hashFixture{
		{"zh deploy", "发布流程是怎样的", zhRel, zhUnrel},
		{"zh backup", "备份怎么确认", zhRel, zhUnrel},
		{"zh rollback", "出现异常怎么回退版本", zhRel, zhUnrel},
		{"zh menu", "午餐供应时间是什么", zhUnrel, zhRel},
		{"zh expense", "报销需要谁审批", zhThird, zhRel},
		{"zh deploy vs expense", "发布流程是怎样的", zhRel, zhThird},
		{"zh short deploy", "部署流程是什么", zhShortRel, zhShortUnrel},
		{"zh short backup", "备份怎么做", zhShortRel, zhShortUnrel},
		{"en release", "how does the release process work", enRel, enUnrel},
		{"en rollback", "what do I do if something looks wrong", enRel, enUnrel},
		{"en menu", "what time is lunch", enUnrel, enRel},
	}
}

// TestHashEmbedding_RanksRelevantAboveUnrelated is the guard on the embedding
// dimension.
//
// Hashed features that collide are indistinguishable, so the dimension has to be
// large relative to what one chunk contributes (~140 features for a 125-rune
// Chinese chunk). At the original 128 dimensions that chunk filled 73% of the
// space and the ranking inverted: an unrelated passage outscored a relevant one
// by 0.28, which is worse than noise because it actively misleads the retrieval.
// Measured, 2048 still inverted one pair (-0.060), 4096 one (-0.025), and 8192
// none; 16384 measured no better.
func TestHashEmbedding_RanksRelevantAboveUnrelated(t *testing.T) {
	dim := NewInMemoryVectorStore().dim
	for _, f := range hashFixtures() {
		rel := float64(cosineSimilarity(hashEmbed(f.query, dim), hashEmbed(f.rel, dim)))
		unrel := float64(cosineSimilarity(hashEmbed(f.query, dim), hashEmbed(f.unrel, dim)))
		if rel <= unrel {
			t.Errorf("%s: the unrelated passage outscored the relevant one (%.4f vs %.4f) at dim=%d — "+
				"raise the dimension rather than lowering the cut-off", f.name, unrel, rel, dim)
		}
	}
}

// TestHashEmbedding_ChunkOccupancy pins the reason the dimension is what it is:
// a chunk-sized text must fill only a small fraction of the space, or collisions
// drown the lexical signal.
func TestHashEmbedding_ChunkOccupancy(t *testing.T) {
	store := NewInMemoryVectorStore()
	// A document chunk is ~400 runes; this fixture is ~125, so it is the easy case.
	text := hashFixtures()[0].rel
	vec := hashEmbed(text, store.dim)
	nonzero := 0
	for _, v := range vec {
		if v != 0 {
			nonzero++
		}
	}
	if occupancy := float64(nonzero) / float64(store.dim); occupancy > 0.10 {
		t.Errorf("a chunk fills %.0f%% of the %d dimensions (%d features); collisions will dominate — "+
			"the original 128 dimensions sat at 73%%", occupancy*100, store.dim, nonzero)
	}
}
