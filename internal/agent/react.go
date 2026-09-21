package agent

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
)

// ReactAgentConfig configures a ReAct agent.
type ReactAgentConfig struct {
	Name          string
	Instruction   string
	ToolNames     []string
	MaxIterations int
}

// ReactAgent wraps an Eino ReAct Agent with project-level configuration.
type ReactAgent struct {
	config ReactAgentConfig
	agent  *react.Agent
}

// Registry is a minimal interface for getting tools by name.
type Registry interface {
	GetToolsForNames(names []string) []tool.BaseTool
}

// NewReactAgent creates a new ReAct agent using Eino's react.NewAgent.
// model must implement model.ToolCallingChatModel (e.g. MockChatModel, openai.ChatModel, ark.ChatModel).
func NewReactAgent(ctx context.Context, config ReactAgentConfig, chatModel model.ToolCallingChatModel, registry Registry) (*ReactAgent, error) {
	einoTools := registry.GetToolsForNames(config.ToolNames)

	agent, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: chatModel,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools: einoTools,
		},
		MaxStep: config.MaxIterations * 2,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Eino ReAct agent %s: %w", config.Name, err)
	}

	return &ReactAgent{
		config: config,
		agent:  agent,
	}, nil
}

// RunResult holds the result of a ReAct agent run.
type RunResult struct {
	Answer      string
	Interrupted bool
	InterruptID string
	ToolName    string
	Arguments   string
	Events      []Event
}

// Run executes the ReAct loop using Eino's agent.Generate.
func (a *ReactAgent) Run(ctx context.Context, messages []*schema.Message, recorder *EventRecorder) RunResult {
	if recorder == nil {
		recorder = NewEventRecorder("unknown")
	}

	recorder.Record(EventAgentStart, fmt.Sprintf("Agent %s started", a.config.Name), map[string]any{
		"agent": a.config.Name, "max_iterations": a.config.MaxIterations,
	})

	result, err := a.agent.Generate(ctx, messages)
	if err != nil {
		recorder.Record(EventAgentEnd, fmt.Sprintf("Agent %s error: %v", a.config.Name, err), nil)
		return RunResult{
			Answer: fmt.Sprintf("Agent 执行出错：%v", err),
			Events: recorder.Events(),
		}
	}

	recorder.Record(EventAgentEnd, fmt.Sprintf("Agent %s finished: %s", a.config.Name, result.Content), nil)
	return RunResult{
		Answer: result.Content,
		Events: recorder.Events(),
	}
}

// Name returns the agent name.
func (a *ReactAgent) Name() string {
	return a.config.Name
}

// Config returns the agent configuration.
func (a *ReactAgent) Config() ReactAgentConfig {
	return a.config
}

// EinoAgent returns the underlying Eino react.Agent for direct access (e.g. streaming).
func (a *ReactAgent) EinoAgent() *react.Agent {
	return a.agent
}
