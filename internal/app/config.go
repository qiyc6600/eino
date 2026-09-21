package app

import (
	"os"
	"strings"
	"sync"
	"time"

	"github.com/joho/godotenv"
)

// Config holds application configuration.
type Config struct {
	// Server configuration
	Addr string // HTTP listen address, default ":8080"

	// Session configuration
	SessionTTL time.Duration // sliding session lifetime, default 30m
	// Initial administrator. Both values are required only while no admin
	// exists in the selected user store.
	BootstrapAdminUsername string
	BootstrapAdminPassword string
	LoginMaxFailures       int
	LoginFailureWindow     time.Duration
	LoginLockout           time.Duration

	// Storage backends: memory by default, single-process JSON file persistence,
	// or PostgreSQL for shared multi-instance state. Paths default under data/.
	UserStoreKind        string
	SessionStoreKind     string
	SessionStorePath     string
	CheckpointStoreKind  string
	CheckpointStorePath  string
	MemoryStoreKind      string
	MemoryStorePath      string
	ThreadStoreKind      string
	ThreadStorePath      string
	ApprovalStoreKind    string
	BusinessStoreKind    string
	DatabaseURL          string
	DatabaseMaxOpen      int
	DatabaseMaxIdle      int
	DatabaseConnLifetime time.Duration
	RunEventRetention    time.Duration

	// Model configuration
	ModelProvider string // mock | openai | ark
	// OpenAI / OpenAI-compatible API
	OpenAIBaseURL string
	OpenAIAPIKey  string
	OpenAIModel   string
	// Ark (ByteDance Volcano Engine)
	ArkAPIKey         string
	ArkModel          string
	ArkBaseURL        string
	EmbeddingProvider string // hash | openai | ollama
	EmbeddingModel    string // model name for embedding provider

	// Context management
	MaxMessages             int
	MaxTokens               int
	SummarizeThresholdRatio float64
	SummaryTargetTokens     int
	// ReserveOutputTokens is held back from the window for the model's answer.
	ReserveOutputTokens int
	// MaxToolResultChars caps a single tool result before it enters the history.
	MaxToolResultChars int

	// Memory retrieval & lifecycle
	MemoryBudgetTokens         int // token budget for memory injection per turn
	MemoryConsolidateThreshold int // active entries before consolidation pays off
	// DocumentBudgetTokens caps the document section of the injected context.
	// It is separate from the memory budget so the two cannot crowd each other
	// out; 0 disables document retrieval.
	DocumentBudgetTokens int
	// VectorMinScore is the relevance cut-off for vector recall. 0 = auto, which
	// picks a value matching the embedder's score scale (the hash fallback scores
	// far lower than real embeddings).
	VectorMinScore float64

	// Thread history governance. Both default to preserving everything: a
	// conversation store that silently drops data is a product decision, not a
	// sensible default.
	//
	// ThreadHistoryMaxMessages caps one conversation's stored history; older
	// messages are dropped on write (0 = unlimited).
	ThreadHistoryMaxMessages int
	// ThreadRetention deletes threads untouched for this long (0 = keep forever).
	ThreadRetention time.Duration

	// SessionCookieSecure marks the browser session cookie Secure. Browsers only
	// store Secure cookies over HTTPS or on localhost, so serving the demo over
	// plain HTTP on a LAN address requires turning this off.
	SessionCookieSecure bool
}

// ModelProfile defines a pre-configured AI model provider profile.
// Users can switch between profiles at runtime via the API.
type ModelProfile struct {
	ID          string // unique identifier: "mock", "deepseek", "qwen", "openai"
	Name        string // display name: "Mock (关键词)", "DeepSeek", "通义千问", "OpenAI"
	Provider    string // underlying provider: "mock" or "openai" (OpenAI-compatible)
	BaseURL     string // API base URL (empty for mock)
	Model       string // model name (empty for mock)
	NeedsAPIKey bool   // whether an API key is required
}

// PresetModelProfiles defines the built-in model profiles.
var PresetModelProfiles = []ModelProfile{
	{
		ID:          "mock",
		Name:        "Mock (关键词匹配)",
		Provider:    "mock",
		NeedsAPIKey: false,
	},
	{
		ID:          "deepseek",
		Name:        "DeepSeek",
		Provider:    "openai",
		BaseURL:     "https://api.deepseek.com/v1",
		Model:       "deepseek-chat",
		NeedsAPIKey: true,
	},
	{
		ID:          "qwen",
		Name:        "通义千问",
		Provider:    "openai",
		BaseURL:     "https://dashscope.aliyuncs.com/compatible-mode/v1",
		Model:       "qwen-plus",
		NeedsAPIKey: true,
	},
	{
		ID:          "openai",
		Name:        "OpenAI",
		Provider:    "openai",
		BaseURL:     "https://api.openai.com/v1",
		Model:       "gpt-4o",
		NeedsAPIKey: true,
	},
}

// ModelRegistry manages available model profiles and their API keys at runtime.
type ModelRegistry struct {
	mu      sync.RWMutex
	apiKeys map[string]string // profileID -> API key
}

// NewModelRegistry creates a new registry with initial keys from config.
func NewModelRegistry() *ModelRegistry {
	return &ModelRegistry{
		apiKeys: make(map[string]string),
	}
}

// SetAPIKey stores an API key for a profile.
func (r *ModelRegistry) SetAPIKey(profileID, key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.apiKeys[profileID] = key
}

// GetAPIKey retrieves the API key for a profile.
func (r *ModelRegistry) GetAPIKey(profileID string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.apiKeys[profileID]
}

// LoadConfig loads configuration from environment variables and .env file.
func LoadConfig() *Config {
	_ = godotenv.Load()

	return &Config{
		Addr:                       getEnv("ADDR", ":8080"),
		SessionTTL:                 getEnvDuration("SESSION_TTL", 30*time.Minute),
		BootstrapAdminUsername:     getEnv("BOOTSTRAP_ADMIN_USERNAME", ""),
		BootstrapAdminPassword:     getEnv("BOOTSTRAP_ADMIN_PASSWORD", ""),
		LoginMaxFailures:           getEnvInt("LOGIN_MAX_FAILURES", 5),
		LoginFailureWindow:         getEnvDuration("LOGIN_FAILURE_WINDOW", 15*time.Minute),
		LoginLockout:               getEnvDuration("LOGIN_LOCKOUT", 15*time.Minute),
		UserStoreKind:              getEnv("USER_STORE", "memory"),
		SessionStoreKind:           getEnv("SESSION_STORE", "memory"),
		SessionStorePath:           getEnv("SESSION_STORE_PATH", "data/sessions.json"),
		CheckpointStoreKind:        getEnv("CHECKPOINT_STORE", "memory"),
		CheckpointStorePath:        getEnv("CHECKPOINT_STORE_PATH", "data/checkpoints.json"),
		MemoryStoreKind:            getEnv("MEMORY_STORE", "memory"),
		MemoryStorePath:            getEnv("MEMORY_STORE_PATH", "data/memory.json"),
		ThreadStoreKind:            getEnv("THREAD_STORE", "memory"),
		ThreadStorePath:            getEnv("THREAD_STORE_PATH", "data/threads.json"),
		ApprovalStoreKind:          getEnv("APPROVAL_STORE", getEnv("CHECKPOINT_STORE", "memory")),
		BusinessStoreKind:          getEnv("BUSINESS_STORE", "memory"),
		DatabaseURL:                getEnv("DATABASE_URL", ""),
		DatabaseMaxOpen:            getEnvInt("DATABASE_MAX_OPEN_CONNS", 25),
		DatabaseMaxIdle:            getEnvInt("DATABASE_MAX_IDLE_CONNS", 5),
		DatabaseConnLifetime:       getEnvDuration("DATABASE_CONN_MAX_LIFETIME", 30*time.Minute),
		RunEventRetention:          getEnvDuration("RUN_EVENT_RETENTION", 7*24*time.Hour),
		ModelProvider:              getEnv("MODEL_PROVIDER", "mock"),
		OpenAIBaseURL:              getEnv("OPENAI_BASE_URL", ""),
		OpenAIAPIKey:               getEnv("OPENAI_API_KEY", ""),
		OpenAIModel:                getEnv("OPENAI_MODEL", ""),
		ArkAPIKey:                  getEnv("ARK_API_KEY", ""),
		ArkModel:                   getEnv("ARK_MODEL", ""),
		ArkBaseURL:                 getEnv("ARK_BASE_URL", ""),
		EmbeddingProvider:          getEnv("EMBEDDING_PROVIDER", "hash"),
		EmbeddingModel:             getEnv("EMBEDDING_MODEL", ""),
		MaxMessages:                getEnvInt("MAX_MESSAGES", 0),
		MaxTokens:                  getEnvInt("MAX_TOKENS", 8000),
		SummarizeThresholdRatio:    getEnvFloat("SUMMARIZE_THRESHOLD_RATIO", 0.8),
		SummaryTargetTokens:        getEnvInt("SUMMARY_TARGET_TOKENS", 800),
		ReserveOutputTokens:        getEnvInt("RESERVE_OUTPUT_TOKENS", 1024),
		MaxToolResultChars:         getEnvInt("MAX_TOOL_RESULT_CHARS", 8000),
		MemoryBudgetTokens:         getEnvInt("MEMORY_BUDGET_TOKENS", 400),
		MemoryConsolidateThreshold: getEnvInt("MEMORY_CONSOLIDATE_THRESHOLD", 30),
		DocumentBudgetTokens:       getEnvInt("DOCUMENT_BUDGET_TOKENS", 800),
		VectorMinScore:             getEnvFloat("VECTOR_MIN_SCORE", 0),
		ThreadHistoryMaxMessages:   getEnvInt("THREAD_HISTORY_MAX_MESSAGES", 0),
		ThreadRetention:            getEnvDuration("THREAD_RETENTION", 0),
		SessionCookieSecure:        getEnvBool("SESSION_COOKIE_SECURE", true),
	}
}

// LoadConfigFromFile loads config from a specific .env file path.
// Useful for testing or when the .env file is in a non-standard location.
func LoadConfigFromFile(path string) *Config {
	_ = godotenv.Load(path)
	return LoadConfig()
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// getEnvDuration parses a Go duration string (e.g. "30m", "2h", "90s").
// Invalid or missing values fall back to the default.
func getEnvDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

func getEnvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n := 0
	for _, c := range v {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		} else {
			return fallback
		}
	}
	return n
}

// getEnvBool reads a boolean flag. Only the common truthy/falsy spellings are
// accepted; anything else falls back rather than guessing.
func getEnvBool(key string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func getEnvFloat(key string, fallback float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	// Simple float parsing: handle "0.8" style values
	intPart := 0
	fracPart := 0
	fracDigits := 0
	afterDot := false
	for _, c := range v {
		if c == '.' {
			afterDot = true
			continue
		}
		if c >= '0' && c <= '9' {
			if afterDot {
				fracPart = fracPart*10 + int(c-'0')
				fracDigits++
			} else {
				intPart = intPart*10 + int(c-'0')
			}
		} else {
			return fallback
		}
	}
	result := float64(intPart)
	if fracDigits > 0 {
		div := 1.0
		for i := 0; i < fracDigits; i++ {
			div *= 10
		}
		result += float64(fracPart) / div
	}
	return result
}
