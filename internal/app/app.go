package app

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/example/agent-eino-demo/internal/agent"
	"github.com/example/agent-eino-demo/internal/auth"
	"github.com/example/agent-eino-demo/internal/contextmgr"
	"github.com/example/agent-eino-demo/internal/hitl"
	"github.com/example/agent-eino-demo/internal/httpapi"
	"github.com/example/agent-eino-demo/internal/memory"
	pgstore "github.com/example/agent-eino-demo/internal/postgres"
	"github.com/example/agent-eino-demo/internal/tools"
)

// App holds all application dependencies.
type App struct {
	Config     *Config
	AuthSvc    *auth.Service
	RBAC       *auth.RBACManager
	MemorySvc  *memory.Service
	Registry   *tools.ToolRegistry
	Summarizer *contextmgr.Summarizer
	Runner     *agent.Runner
	Router     *httpapi.Router
	HITLSvc    *hitl.Service
	Postgres   *pgstore.Backend

	// Model switching
	registry     *ModelRegistry
	chatModel    model.ToolCallingChatModel
	currentModel string // current model profile ID
}

// NewApp creates and wires all application components.
func NewApp(cfg *Config) *App {
	ctx := context.Background()
	var pg *pgstore.Backend
	if usesPostgres(cfg) {
		connectCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		var err error
		pg, err = pgstore.Open(connectCtx, cfg.DatabaseURL, cfg.DatabaseMaxOpen, cfg.DatabaseMaxIdle, cfg.DatabaseConnLifetime)
		if err != nil {
			panic(fmt.Errorf("initialize postgres: %w", err))
		}
	}

	// 1. Auth — SessionStore backend is switchable via SESSION_STORE
	sessionStore, err := newSessionStore(cfg, pg)
	if err != nil {
		panic(fmt.Errorf("initialize session store: %w", err))
	}
	userStore, err := newUserStore(cfg, pg)
	if err != nil {
		panic(fmt.Errorf("initialize user store: %w", err))
	}
	rbac := auth.NewRBACManager()
	authSvc, err := auth.NewServiceWithUserStore(sessionStore, userStore, rbac, cfg.SessionTTL)
	if err != nil {
		panic(fmt.Errorf("initialize auth service: %w", err))
	}
	if err := authSvc.EnsureBootstrapAdmin(ctx, cfg.BootstrapAdminUsername, cfg.BootstrapAdminPassword); err != nil {
		panic(fmt.Errorf("initialize administrator: %w", err))
	}
	var loginLimitStore auth.LoginLimitStore = auth.NewInMemoryLoginLimitStore()
	if pg != nil {
		loginLimitStore = pg.LoginLimits
	}
	limiter, err := auth.NewLoginLimiter(loginLimitStore, auth.LoginRatePolicy{
		MaxFailures: cfg.LoginMaxFailures, Window: cfg.LoginFailureWindow, Lockout: cfg.LoginLockout,
	})
	if err != nil {
		panic(fmt.Errorf("initialize login limiter: %w", err))
	}
	authSvc.SetLoginLimiter(limiter)

	// 2. Tools
	toolRegistry := tools.NewToolRegistry()
	orderStore, emailStore, err := newBusinessStores(ctx, cfg, pg)
	if err != nil {
		panic(fmt.Errorf("initialize business stores: %w", err))
	}
	toolRegistry.Register(tools.NewCalculatorTool())
	toolRegistry.Register(tools.NewWeatherTool())
	toolRegistry.Register(tools.NewGrepTool())
	toolRegistry.Register(tools.NewQueryOrderTool(orderStore))
	toolRegistry.Register(tools.NewDeleteOrderTool(orderStore))
	toolRegistry.Register(tools.NewSendEmailTool(emailStore))

	// 3. ACL
	aclMiddleware := tools.NewACLMiddleware(rbac)
	aclMiddleware.WrapAllTools(toolRegistry)

	// 4. HITL — CheckpointStore backend is switchable via CHECKPOINT_STORE
	checkpointStore, err := newCheckpointStore(cfg, pg)
	if err != nil {
		panic(fmt.Errorf("initialize checkpoint store: %w", err))
	}
	interruptMgr := hitl.NewInterruptManager(checkpointStore)
	if cfg.ApprovalStoreKind == "postgres" {
		if pg == nil {
			panic("approval store is postgres but postgres backend is unavailable")
		}
		interruptMgr.UseStore(pg.Approvals)
	} else if cfg.ApprovalStoreKind == "file" {
		if err := interruptMgr.UseFile(cfg.CheckpointStorePath + ".approvals.json"); err != nil {
			panic(fmt.Errorf("load approvals: %w", err))
		}
	} else if cfg.ApprovalStoreKind != "memory" {
		panic(fmt.Errorf("invalid APPROVAL_STORE %q: must be memory, file or postgres", cfg.ApprovalStoreKind))
	}
	hitlSvc := hitl.NewService(interruptMgr, checkpointStore, rbac)

	// 5. Memory — create chatModel first so memory service can use it for LLM extraction
	modelReg := NewModelRegistry()
	chatModel := createChatModel(ctx, cfg)

	// Determine initial profile ID from config
	currentModel := cfg.ModelProvider
	if cfg.ModelProvider == "openai" {
		// Try to match a preset profile by base URL
		currentModel = matchProfileFromConfig(cfg)
	}

	// Store the API key from config into registry
	if cfg.OpenAIAPIKey != "" {
		modelReg.SetAPIKey(currentModel, cfg.OpenAIAPIKey)
	}

	// Memory (long-term KV + short-term checkpoint + vector retrieval)
	// MemoryStore backend is switchable via MEMORY_STORE
	memoryStore, err := newMemoryStore(cfg, pg)
	if err != nil {
		panic(fmt.Errorf("initialize memory store: %w", err))
	}
	var vectorStore memory.VectorStore
	switch cfg.EmbeddingProvider {
	case "openai":
		embedModel := cfg.EmbeddingModel
		if embedModel == "" {
			embedModel = "text-embedding-3-small"
		}
		baseURL := cfg.OpenAIBaseURL
		apiKey := cfg.OpenAIAPIKey
		vs, err := memory.NewChromemVectorStoreWithOpenAI(embedModel, baseURL, apiKey)
		if err != nil {
			log.Printf("Warning: Failed to create OpenAI vector store: %v, falling back to hash-based", err)
			vectorStore = memory.NewInMemoryVectorStore()
		} else {
			vectorStore = vs
			log.Printf("Using ChromemVectorStore with OpenAI embeddings (model=%s)", embedModel)
		}
	case "ollama":
		embedModel := cfg.EmbeddingModel
		if embedModel == "" {
			embedModel = "nomic-embed-text"
		}
		vs, err := memory.NewChromemVectorStoreWithOllama(embedModel)
		if err != nil {
			log.Printf("Warning: Failed to create Ollama vector store: %v, falling back to hash-based", err)
			vectorStore = memory.NewInMemoryVectorStore()
		} else {
			vectorStore = vs
			log.Printf("Using ChromemVectorStore with Ollama embeddings (model=%s)", embedModel)
		}
	default:
		vectorStore = memory.NewInMemoryVectorStore()
		log.Printf("Using InMemoryVectorStore (hash-based pseudo-embeddings)")
	}
	memorySvc := memory.NewService(memoryStore, checkpointStore, vectorStore, chatModel)
	memorySvc.SetRetrievalConfig(cfg.MemoryBudgetTokens, cfg.MemoryConsolidateThreshold)

	// 6. Context management
	tokenCounter := contextmgr.NewSimpleTokenCounter()
	summarizer := contextmgr.NewSummarizer(tokenCounter, cfg.SummarizeThresholdRatio, cfg.SummaryTargetTokens, chatModel)

	// 7. Agent — multi-agent with supervisor routing for all modes
	registryAdapter := tools.NewEinoRegistryAdapter(toolRegistry)

	supervisor, err := agent.BuildDefaultSupervisor(ctx, chatModel, registryAdapter)
	if err != nil {
		log.Fatalf("Failed to build supervisor: %v", err)
	}

	steppedRunner := agent.NewSteppedRunner(chatModel, toolRegistry, hitlSvc, 20,
		buildDispatchEntries(supervisor, toolRegistry, chatModel, hitlSvc, rbac, ctx), rbac)
	// Node-level interrupt is available but not enabled by default.
	// It is activated by the explicit per-request confirmBeforeExecute flag.
	runner := agent.NewRunner(supervisor, steppedRunner, hitlSvc, toolRegistry, rbac, memorySvc, summarizer, cfg.MaxTokens)
	if pg != nil {
		runner.UseRunStore(pg.Runs, cfg.RunEventRetention)
	}

	// Conversation threads are switchable via THREAD_STORE. File survives
	// restarts in one process; PostgreSQL coordinates multiple instances.
	if cfg.ThreadStoreKind == "postgres" {
		if pg == nil {
			panic("thread store is postgres but postgres backend is unavailable")
		}
		runner.UseThreadStore(pg.Threads)
	} else if cfg.ThreadStoreKind == "file" {
		if err := runner.UseFileThreads(cfg.ThreadStorePath); err != nil {
			panic(fmt.Errorf("initialize thread store: %w", err))
		}
	}
	if cfg.CheckpointStoreKind == "postgres" && cfg.ThreadStoreKind == "postgres" && cfg.ApprovalStoreKind == "postgres" {
		runner.UseInterruptPublisher(pg)
		runner.UseResumePublisher(pg)
	}

	// Build App first so the router can reference it for model switching
	a := &App{
		Config:       cfg,
		AuthSvc:      authSvc,
		RBAC:         rbac,
		MemorySvc:    memorySvc,
		Registry:     toolRegistry,
		Summarizer:   summarizer,
		Runner:       runner,
		HITLSvc:      hitlSvc,
		Postgres:     pg,
		registry:     modelReg,
		chatModel:    chatModel,
		currentModel: currentModel,
	}

	// 8. Router (needs App for model switching)
	a.Router = httpapi.NewRouter(authSvc, runner, hitlSvc, memorySvc, toolRegistry, a, a)

	return a
}

// Close releases external storage pools. It is safe to call on memory/file deployments.
func (a *App) Close() error {
	if a.Postgres != nil {
		return a.Postgres.Close()
	}
	return nil
}

// Ready reports whether dependencies required to accept traffic are available.
func (a *App) Ready(ctx context.Context) error {
	if a.Postgres != nil {
		return a.Postgres.Ready(ctx)
	}
	return nil
}

// SwitchModel switches the active model to the specified profile.
// This rebuilds the agent and updates all dependent components.
func (a *App) SwitchModel(profileID, apiKey string) error {
	// Find the profile
	var profile ModelProfile
	found := false
	for _, p := range PresetModelProfiles {
		if p.ID == profileID {
			profile = p
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("unknown model profile: %s", profileID)
	}

	// Validate API key
	if profile.NeedsAPIKey && apiKey == "" {
		// Try to use stored key
		apiKey = a.registry.GetAPIKey(profileID)
		if apiKey == "" {
			return fmt.Errorf("API key is required for %s", profile.Name)
		}
	}

	// Store the key
	if apiKey != "" {
		a.registry.SetAPIKey(profileID, apiKey)
	}

	// Create new chat model
	ctx := context.Background()
	newModel, err := createChatModelFromProfile(ctx, profile, apiKey)
	if err != nil {
		return fmt.Errorf("failed to create model: %w", err)
	}

	// Rebuild agent — multi-agent with supervisor routing for all modes
	registryAdapter := tools.NewEinoRegistryAdapter(a.Registry)

	supervisor, err := agent.BuildDefaultSupervisor(ctx, newModel, registryAdapter)
	if err != nil {
		return fmt.Errorf("failed to build supervisor: %w", err)
	}

	// Update runner
	a.Runner.SetSupervisor(supervisor)

	// Rebuild SteppedRunner with new dispatch entries
	newSteppedRunner := agent.NewSteppedRunner(newModel, a.Registry, a.HITLSvc, 20,
		buildDispatchEntries(supervisor, a.Registry, newModel, a.HITLSvc, a.RBAC, ctx), a.RBAC)
	a.Runner.SetSteppedRunner(newSteppedRunner)

	// Update summarizer
	a.Summarizer.SetChatModel(newModel)

	// Update memory service
	a.MemorySvc.SetChatModel(newModel)

	// Update app state
	a.chatModel = newModel
	a.currentModel = profileID

	log.Printf("Model switched to: %s (%s)", profile.Name, profileID)
	return nil
}

// CurrentModel returns the current model profile ID.
func (a *App) CurrentModel() string {
	return a.currentModel
}

// GetModelRegistry returns the model registry.
func (a *App) GetModelRegistry() *ModelRegistry {
	return a.registry
}

// ListModelProfiles returns all preset model profiles with API key status.
// This satisfies the httpapi.ModelSwitcher interface.
func (a *App) ListModelProfiles() []httpapi.ModelProfileInfo {
	profiles := PresetModelProfiles
	result := make([]httpapi.ModelProfileInfo, 0, len(profiles))

	for _, p := range profiles {
		hasKey := false
		if a.registry != nil && a.registry.GetAPIKey(p.ID) != "" {
			hasKey = true
		}
		result = append(result, httpapi.ModelProfileInfo{
			ID:          p.ID,
			Name:        p.Name,
			Provider:    p.Provider,
			NeedsAPIKey: p.NeedsAPIKey,
			HasAPIKey:   hasKey,
		})
	}
	return result
}

// Start starts the HTTP server.
func (a *App) Start() error {
	handler := a.Router.Handler()

	log.Printf("Starting server on %s", a.Config.Addr)
	log.Printf("Model provider: %s (Eino react.Agent)", a.currentModel)
	return http.ListenAndServe(a.Config.Addr, handler)
}

// matchProfileFromConfig tries to match the current config to a preset profile.
func matchProfileFromConfig(cfg *Config) string {
	for _, p := range PresetModelProfiles {
		if p.Provider == "openai" && p.BaseURL == cfg.OpenAIBaseURL && p.Model == cfg.OpenAIModel {
			return p.ID
		}
	}
	// Default: if using openai provider, use "openai" as profile ID
	if cfg.ModelProvider == "openai" {
		return "openai"
	}
	return cfg.ModelProvider
}

// buildDispatchEntries constructs the dispatch table for the SteppedRunner.
// When sub-agents exist, it also creates sub-agent SteppedRunners with HITL support.
func buildDispatchEntries(
	supervisor *agent.SupervisorAgent,
	registry *tools.ToolRegistry,
	chatModel model.ToolCallingChatModel,
	hitlSvc *hitl.Service,
	rbac *auth.RBACManager,
	ctx context.Context,
) []*agent.DispatchEntry {
	wrappers := supervisor.AgentToolWrappers()
	if len(wrappers) > 0 {
		// Sub-agent routing: create a SteppedRunner per sub-agent for HITL support
		var entries []*agent.DispatchEntry
		for _, wrapper := range wrappers {
			info, _ := wrapper.Info(ctx)

			// Build dispatch entries for this sub-agent's real tools
			subAgentConfig := wrapper.SubAgentConfig()
			var subEntries []*agent.DispatchEntry
			for _, toolName := range subAgentConfig.ToolNames {
				rt, ok := registry.Get(toolName)
				if !ok {
					continue
				}
				einoTool := tools.NewEinoTool(rt)
				toolInfo, _ := einoTool.Info(ctx)
				subEntries = append(subEntries, &agent.DispatchEntry{
					Info:             toolInfo,
					IsSubAgent:       false,
					RequiresApproval: rt.Meta.RequiresApproval,
					ToolName:         rt.Meta.Name,
				})
			}

			// Create a SteppedRunner for this sub-agent
			if len(subEntries) > 0 {
				wrapper.SetSteppedRunner(agent.NewSteppedRunner(chatModel, registry, hitlSvc, 20, subEntries, rbac))
			}

			entries = append(entries, &agent.DispatchEntry{
				Info:         info,
				IsSubAgent:   true,
				AgentWrapper: wrapper,
			})
		}
		return entries
	}

	// No sub-agents: direct tool dispatch
	var entries []*agent.DispatchEntry
	for _, rt := range registry.List() {
		einoTool := tools.NewEinoTool(rt)
		info, _ := einoTool.Info(ctx)
		entries = append(entries, &agent.DispatchEntry{
			Info:             info,
			IsSubAgent:       false,
			RequiresApproval: rt.Meta.RequiresApproval,
			ToolName:         rt.Meta.Name,
		})
	}
	return entries
}

// newSessionStore builds the SessionStore backend selected by SESSION_STORE.
func newSessionStore(cfg *Config, pg *pgstore.Backend) (auth.SessionStore, error) {
	switch cfg.SessionStoreKind {
	case "file":
		return auth.NewFileSessionStore(cfg.SessionStorePath)
	case "memory":
		return auth.NewInMemorySessionStore(), nil
	case "postgres":
		if pg == nil {
			return nil, fmt.Errorf("postgres backend is unavailable")
		}
		return pg.Sessions, nil
	default:
		return nil, fmt.Errorf("invalid SESSION_STORE %q: must be memory, file or postgres", cfg.SessionStoreKind)
	}
}

func newUserStore(cfg *Config, pg *pgstore.Backend) (auth.UserStore, error) {
	switch cfg.UserStoreKind {
	case "memory":
		return auth.NewInMemoryUserStore(), nil
	case "postgres":
		if pg == nil {
			return nil, fmt.Errorf("postgres backend is unavailable")
		}
		return pg.Users, nil
	default:
		return nil, fmt.Errorf("invalid USER_STORE %q: must be memory or postgres", cfg.UserStoreKind)
	}
}

// newCheckpointStore builds the CheckpointStore backend selected by CHECKPOINT_STORE.
func newCheckpointStore(cfg *Config, pg *pgstore.Backend) (memory.CheckpointStore, error) {
	switch cfg.CheckpointStoreKind {
	case "file":
		return memory.NewFileCheckpointStore(cfg.CheckpointStorePath)
	case "memory":
		return memory.NewInMemoryCheckpointStore(), nil
	case "postgres":
		if pg == nil {
			return nil, fmt.Errorf("postgres backend is unavailable")
		}
		return pg.Checkpoints, nil
	default:
		return nil, fmt.Errorf("invalid CHECKPOINT_STORE %q: must be memory, file or postgres", cfg.CheckpointStoreKind)
	}
}

// newMemoryStore builds the MemoryStore backend selected by MEMORY_STORE.
func newMemoryStore(cfg *Config, pg *pgstore.Backend) (memory.MemoryStore, error) {
	switch cfg.MemoryStoreKind {
	case "file":
		return memory.NewFileMemoryStore(cfg.MemoryStorePath)
	case "memory":
		return memory.NewInMemoryMemoryStore(), nil
	case "postgres":
		if pg == nil {
			return nil, fmt.Errorf("postgres backend is unavailable")
		}
		return pg.Memories, nil
	default:
		return nil, fmt.Errorf("invalid MEMORY_STORE %q: must be memory, file or postgres", cfg.MemoryStoreKind)
	}
}

func newBusinessStores(ctx context.Context, cfg *Config, pg *pgstore.Backend) (tools.OrderRepository, tools.EmailRepository, error) {
	switch cfg.BusinessStoreKind {
	case "memory":
		return tools.NewOrderStore(), tools.NewEmailStore(), nil
	case "postgres":
		if pg == nil {
			return nil, nil, fmt.Errorf("postgres backend is unavailable")
		}
		seedCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if err := pg.Orders.Seed(seedCtx, tools.DefaultOrders()); err != nil {
			return nil, nil, err
		}
		return pg.Orders, pg.Emails, nil
	default:
		return nil, nil, fmt.Errorf("invalid BUSINESS_STORE %q: must be memory or postgres", cfg.BusinessStoreKind)
	}
}

func usesPostgres(cfg *Config) bool {
	return cfg.UserStoreKind == "postgres" || cfg.SessionStoreKind == "postgres" || cfg.CheckpointStoreKind == "postgres" ||
		cfg.MemoryStoreKind == "postgres" || cfg.ThreadStoreKind == "postgres" || cfg.ApprovalStoreKind == "postgres" ||
		cfg.BusinessStoreKind == "postgres"
}
