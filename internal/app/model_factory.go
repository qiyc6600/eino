package app

import (
	"context"
	"fmt"
	"log"

	"github.com/cloudwego/eino/components/model"
	"github.com/example/agent-eino-demo/internal/agent"
)

// createChatModel creates a ChatModel based on the configuration.
// Returns a model.ToolCallingChatModel that can be used with Eino's react.NewAgent.
func createChatModel(ctx context.Context, cfg *Config) model.ToolCallingChatModel {
	switch cfg.ModelProvider {
	case "mock":
		log.Printf("Using MockChatModel (no real LLM, keyword matching only)")
		return agent.NewMockChatModel()
	case "openai":
		if cfg.OpenAIBaseURL == "" {
			log.Fatal("OPENAI_BASE_URL is required when MODEL_PROVIDER=openai")
		}
		if cfg.OpenAIAPIKey == "" {
			log.Fatal("OPENAI_API_KEY is required when MODEL_PROVIDER=openai")
		}
		if cfg.OpenAIModel == "" {
			log.Fatal("OPENAI_MODEL is required when MODEL_PROVIDER=openai")
		}

		chatModel, err := newOpenAIChatModelImpl(ctx, cfg)
		if err != nil {
			log.Fatalf("Failed to create OpenAI ChatModel: %v", err)
		}

		log.Printf("Using OpenAI-compatible (%s) model: %s", cfg.OpenAIBaseURL, cfg.OpenAIModel)
		return chatModel
	case "ark":
		if cfg.ArkAPIKey == "" {
			log.Fatal("ARK_API_KEY is required when MODEL_PROVIDER=ark")
		}
		if cfg.ArkModel == "" {
			log.Fatal("ARK_MODEL is required when MODEL_PROVIDER=ark")
		}

		chatModel, err := newArkChatModelImpl(ctx, cfg)
		if err != nil {
			log.Fatalf("Failed to create Ark ChatModel: %v", err)
		}

		log.Printf("Using Ark model: %s", cfg.ArkModel)
		return chatModel
	default:
		log.Fatalf("Unknown MODEL_PROVIDER: %s (must be mock, openai, or ark)", cfg.ModelProvider)
		return nil
	}
}

// createChatModelFromProfile creates a ChatModel from a ModelProfile and API key.
// This is used for runtime model switching.
func createChatModelFromProfile(ctx context.Context, profile ModelProfile, apiKey string) (model.ToolCallingChatModel, error) {
	switch profile.Provider {
	case "mock":
		log.Printf("Switching to MockChatModel")
		return agent.NewMockChatModel(), nil
	case "openai":
		if profile.BaseURL == "" {
			return nil, fmt.Errorf("base URL is required for provider %s", profile.ID)
		}
		if apiKey == "" {
			return nil, fmt.Errorf("API key is required for provider %s", profile.ID)
		}
		if profile.Model == "" {
			return nil, fmt.Errorf("model name is required for provider %s", profile.ID)
		}

		cfg := &Config{
			OpenAIBaseURL: profile.BaseURL,
			OpenAIAPIKey:  apiKey,
			OpenAIModel:   profile.Model,
		}
		chatModel, err := newOpenAIChatModelImpl(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to create %s ChatModel: %w", profile.Name, err)
		}

		log.Printf("Switched to %s (%s, model: %s)", profile.Name, profile.BaseURL, profile.Model)
		return chatModel, nil
	default:
		return nil, fmt.Errorf("unsupported provider: %s", profile.Provider)
	}
}
