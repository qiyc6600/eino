package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
)

// SupervisorAgent routes user requests to specialized sub-agents.
// It uses Eino's AgentAsTool pattern: sub-agents are wrapped as tools.
type SupervisorAgent struct {
	chatModel     model.ToolCallingChatModel
	agents        map[string]*ReactAgent
	supAgent      *react.Agent
	registry      Registry
	descriptions  map[string]string   // detailed tool descriptions for the LLM
	agentWrappers []*agentToolWrapper // stored for SteppedRunner access
}

// NewSupervisorAgent creates a supervisor with the AgentAsTool pattern.
func NewSupervisorAgent(ctx context.Context, chatModel model.ToolCallingChatModel, registry Registry) (*SupervisorAgent, error) {
	return &SupervisorAgent{
		chatModel:    chatModel,
		agents:       make(map[string]*ReactAgent),
		registry:     registry,
		descriptions: make(map[string]string),
	}, nil
}

// AddSubAgent adds a sub-agent.
func (s *SupervisorAgent) AddSubAgent(ctx context.Context, config ReactAgentConfig) error {
	agent, err := NewReactAgent(ctx, config, s.chatModel, s.registry)
	if err != nil {
		return fmt.Errorf("failed to create sub-agent %s: %w", config.Name, err)
	}
	s.agents[config.Name] = agent
	return nil
}

// Compile builds the supervisor's Eino ReAct agent with sub-agents as tools.
func (s *SupervisorAgent) Compile(ctx context.Context) error {
	var agentTools []tool.BaseTool
	s.agentWrappers = make([]*agentToolWrapper, 0, len(s.agents))

	for name, a := range s.agents {
		// Use detailed description if available, otherwise fall back to instruction
		desc := a.Config().Instruction
		if d, ok := s.descriptions[name]; ok && d != "" {
			desc = d
		}

		wrapper := &agentToolWrapper{
			info: &schema.ToolInfo{
				Name: name,
				Desc: desc,
				ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
					"message": {Type: schema.String, Desc: "The user's question or request to handle"},
				}),
			},
			agent: a,
		}
		s.agentWrappers = append(s.agentWrappers, wrapper)
		agentTools = append(agentTools, wrapper)
	}

	supAgent, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: s.chatModel,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools: agentTools,
		},
		MaxStep: 20,
	})
	if err != nil {
		return fmt.Errorf("failed to create supervisor agent: %w", err)
	}

	s.supAgent = supAgent
	return nil
}

// SupervisorRunResult holds the result of a supervisor run.
type SupervisorRunResult struct {
	Answer      string
	Interrupted bool
	InterruptID string
	ToolName    string
	Arguments   string
	RoutedAgent string
	Events      []Event
}

// WrapSingleAgent wraps a single ReactAgent as a SupervisorAgent.
// This is used for real LLMs where a single agent with all tools is more reliable
// than a supervisor routing to sub-agents.
func WrapSingleAgent(singleAgent *ReactAgent) *SupervisorAgent {
	return &SupervisorAgent{
		chatModel: nil, // not used — the agent has its own model
		agents:    map[string]*ReactAgent{"assistant": singleAgent},
		supAgent:  nil, // Run/Stream will delegate to the single agent directly
	}
}

// AgentToolWrappers returns the sub-agent wrappers for use by the SteppedRunner.
// Returns nil if in single-agent mode (no sub-agents).
func (s *SupervisorAgent) AgentToolWrappers() []*agentToolWrapper {
	return s.agentWrappers
}

// Run overrides: if supAgent is nil but we have a single wrapped agent, delegate to it.
func (s *SupervisorAgent) Run(ctx context.Context, messages []*schema.Message, recorder *EventRecorder) SupervisorRunResult {
	if recorder == nil {
		recorder = NewEventRecorder("unknown")
	}

	// If compiled normally, use the supervisor's ReAct agent
	if s.supAgent != nil {
		return s.runSupervisor(ctx, messages, recorder)
	}

	// Single agent mode — delegate directly
	if agent, ok := s.agents["assistant"]; ok {
		recorder.Record(EventAgentStart, "Single agent started", nil)
		result := agent.Run(ctx, messages, recorder)
		recorder.Record(EventAgentEnd, fmt.Sprintf("Agent finished: %s", result.Answer), nil)
		return SupervisorRunResult{
			Answer:      result.Answer,
			Interrupted: result.Interrupted,
			InterruptID: result.InterruptID,
			ToolName:    result.ToolName,
			Arguments:   result.Arguments,
			RoutedAgent: "assistant",
			Events:      recorder.Events(),
		}
	}

	return SupervisorRunResult{
		Answer: "No agent available",
		Events: recorder.Events(),
	}
}

// Stream overrides: if supAgent is nil, delegate to the single agent's internal agent.
func (s *SupervisorAgent) Stream(ctx context.Context, messages []*schema.Message) (*schema.StreamReader[*schema.Message], error) {
	if s.supAgent != nil {
		return s.supAgent.Stream(ctx, messages)
	}
	// Single agent mode — delegate to the wrapped agent's Eino react.Agent
	if agent, ok := s.agents["assistant"]; ok {
		return agent.EinoAgent().Stream(ctx, messages)
	}
	return nil, fmt.Errorf("no agent available for streaming")
}

// runSupervisor is the original Run logic for compiled supervisor mode.
func (s *SupervisorAgent) runSupervisor(ctx context.Context, messages []*schema.Message, recorder *EventRecorder) SupervisorRunResult {
	recorder.Record(EventAgentStart, "Supervisor started", nil)

	result, err := s.supAgent.Generate(ctx, messages)
	if err != nil {
		recorder.Record(EventAgentEnd, fmt.Sprintf("Supervisor error: %v", err), nil)
		return SupervisorRunResult{
			Answer: fmt.Sprintf("Supervisor 执行出错：%v", err),
			Events: recorder.Events(),
		}
	}

	routedAgent := ""
	if len(result.ToolCalls) > 0 {
		routedAgent = result.ToolCalls[0].Function.Name
	}

	recorder.Record(EventSupervisorRoute, fmt.Sprintf("Routed to %s", routedAgent), map[string]any{
		"agent": routedAgent,
	})
	recorder.Record(EventAgentEnd, fmt.Sprintf("Supervisor finished: %s", result.Content), nil)

	return SupervisorRunResult{
		Answer:      result.Content,
		RoutedAgent: routedAgent,
		Events:      recorder.Events(),
	}
}

// agentToolWrapper implements tool.InvokableTool by delegating to a ReactAgent.
// When a steppedRunner is set, it uses that instead of ReactAgent.Run()
// to enable HITL interrupt gates inside the sub-agent.
type agentToolWrapper struct {
	info          *schema.ToolInfo
	agent         *ReactAgent
	steppedRunner *SteppedRunner // optional: enables HITL inside sub-agent
}

// SubAgentConfig returns the wrapped ReactAgent's configuration.
func (t *agentToolWrapper) SubAgentConfig() ReactAgentConfig {
	return t.agent.Config()
}

// SetSteppedRunner sets the SteppedRunner for this sub-agent, enabling HITL interrupts.
func (t *agentToolWrapper) SetSteppedRunner(sr *SteppedRunner) {
	t.steppedRunner = sr
}

func (t *agentToolWrapper) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return t.info, nil
}

func (t *agentToolWrapper) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	var a struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &a); err != nil {
		return "", fmt.Errorf("invalid arguments for agent tool: %w", err)
	}

	messages := []*schema.Message{
		schema.SystemMessage(t.agent.Config().Instruction),
		schema.UserMessage(a.Message),
	}

	// Use SteppedRunner if available — enables HITL interrupts inside the sub-agent
	if t.steppedRunner != nil {
		schemaMsgs := toSchemaMessages(messages)
		state := &SteppedRunState{
			Step:     0,
			Messages: schemaMsgs,
			Done:     false,
			RunID:    "sub_" + t.agent.Name(),
			ThreadID: "",
		}

		recorder := NewEventRecorder("sub_" + t.agent.Name())

		// Run step-by-step, checking for interrupts at each step
		for !state.Done {
			var interruptReq *InterruptRequest
			var stepErr error
			state, interruptReq, stepErr = t.steppedRunner.RunStep(ctx, state, recorder, false)
			if stepErr != nil {
				return "", stepErr
			}
			if interruptReq != nil {
				// HITL interrupt inside sub-agent — propagate up
				return "", &SubAgentInterruptError{
					AgentName: t.agent.Name(),
					ToolName:  interruptReq.ToolName,
					Arguments: interruptReq.Arguments,
				}
			}
		}

		return state.Answer, nil
	}

	// Fallback: use Eino's black-box ReAct (no HITL support)
	result := t.agent.Run(ctx, messages, NewEventRecorder("sub_"+t.agent.Name()))
	if result.Interrupted {
		return "", &SubAgentInterruptError{
			AgentName: t.agent.Name(),
			ToolName:  result.ToolName,
			Arguments: result.Arguments,
		}
	}

	return result.Answer, nil
}

// BuildDefaultSupervisor creates a supervisor with the standard 3 sub-agents.
func BuildDefaultSupervisor(ctx context.Context, chatModel model.ToolCallingChatModel, registry Registry) (*SupervisorAgent, error) {
	sup, err := NewSupervisorAgent(ctx, chatModel, registry)
	if err != nil {
		return nil, err
	}

	agents := []ReactAgentConfig{
		{
			Name:          "math_agent",
			Instruction:   "你是一个数学助手，擅长数学计算。请使用 calculator 工具来帮助用户完成计算。",
			ToolNames:     []string{"calculator"},
			MaxIterations: 10,
		},
		{
			Name:          "search_agent",
			Instruction:   "你是一个搜索助手，擅长查询天气和搜索日志信息。请使用 weather 和 grep 工具来帮助用户。",
			ToolNames:     []string{"weather", "grep"},
			MaxIterations: 10,
		},
		{
			Name: "general_agent",
			Instruction: "你是一个通用业务助手，负责处理订单查询、删除订单、发送邮件。可用工具：query_order（查询订单）、delete_order（删除订单）、send_email（发送邮件）。\n\n" +
				"核心规则（必须严格遵守）：\n" +
				"- 必须直接调用工具完成任务，禁止用文字要求用户确认。系统内置审批机制：delete_order、send_email 等高危操作在工具执行前会自动触发人工审批，无需你自行询问用户。\n" +
				"- 当用户要求删除订单时，从用户消息中提取订单号（如 A-1001、B-2003），立即调用 delete_order 工具，参数为 {\"order_id\": \"<订单号>\"}。不要回复\"是否确认删除\"之类的话。\n" +
				"- 当用户要求发送邮件时，提取收件人邮箱和内容，立即调用 send_email 工具。\n" +
				"- 当用户要求查询订单时，调用 query_order 工具。\n" +
				"- 调用工具后，根据工具返回结果用自然语言回复用户。",
			ToolNames:     []string{"query_order", "delete_order", "send_email"},
			MaxIterations: 10,
		},
	}

	// Detailed tool descriptions for the supervisor — tells the LLM exactly when to route
	agentDescriptions := map[string]string{
		"math_agent":    "数学计算助手。当用户需要计算、算术运算、数学问题时调用。可用工具：calculator。",
		"search_agent":  "信息搜索助手。当用户需要查询天气、搜索日志、查找信息时调用。可用工具：weather（天气查询）、grep（日志搜索）。",
		"general_agent": "通用业务助手。当用户需要查询订单、删除订单、发送邮件时调用。可用工具：query_order、delete_order（需审批）、send_email（需审批）。",
	}

	for _, cfg := range agents {
		if err := sup.AddSubAgent(ctx, cfg); err != nil {
			return nil, err
		}
	}

	// Store descriptions so Compile can use them
	sup.descriptions = agentDescriptions

	if err := sup.Compile(ctx); err != nil {
		return nil, err
	}

	return sup, nil
}
