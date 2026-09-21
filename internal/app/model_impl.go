package app

import (
	"context"
	"fmt"
	"net/http"

	openai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
)

// newOpenAIChatModelImpl creates an OpenAI-compatible ChatModel using eino-ext.
//
// Supports any OpenAI-compatible API by setting OPENAI_BASE_URL:
//
//	Provider        | OPENAI_BASE_URL                        | OPENAI_MODEL
//	----------------|----------------------------------------|--------------------
//	OpenAI          | (empty, uses default)                  | gpt-4o
//	DeepSeek        | https://api.deepseek.com               | deepseek-chat
//	Moonshot        | https://api.moonshot.cn/v1             | moonshot-v1-8k
//	ZhipuAI (GLM)   | https://open.bigmodel.cn/api/paas/v4   | glm-4-flash
//	SiliconFlow     | https://api.siliconflow.cn/v1          | Qwen/Qwen2.5-7B-Instruct
//	Local Ollama    | http://localhost:11434/v1               | llama3
func newOpenAIChatModelImpl(ctx context.Context, cfg *Config) (model.ToolCallingChatModel, error) {
	// Build an HTTP client that injects the ksyun-code-type header
	// into every request. This is required by some API gateways.
	httpClient := &http.Client{
		Transport: &headerInjectTransport{
			base: http.DefaultTransport,
			headers: map[string]string{
				"ksyun-code-type": "eino-agent",
			},
		},
	}

	chatModel, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		BaseURL:    cfg.OpenAIBaseURL,
		APIKey:     cfg.OpenAIAPIKey,
		Model:      cfg.OpenAIModel,
		HTTPClient: httpClient,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create OpenAI ChatModel: %w", err)
	}
	return chatModel, nil
}

func newArkChatModelImpl(ctx context.Context, cfg *Config) (model.ToolCallingChatModel, error) {
	// Ark model support requires additional dependency:
	//   go get github.com/cloudwego/eino-ext/components/model/ark
	return nil, fmt.Errorf(
		"Ark model not available: please install:\n" +
			"  go get github.com/cloudwego/eino-ext/components/model/ark",
	)
}

// headerInjectTransport wraps an http.RoundTripper and injects custom headers
// into every outgoing request.
type headerInjectTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

// RoundTrip implements http.RoundTripper.RoundTrip.
func (t *headerInjectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for key, value := range t.headers {
		req.Header.Set(key, value)
	}
	return t.base.RoundTrip(req)
}
