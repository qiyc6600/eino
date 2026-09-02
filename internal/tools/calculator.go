package tools

import (
	"encoding/json"
	"fmt"
	"github.com/example/agent-eino-demo/internal/auth"
	"strconv"
	"strings"
)

// CalculatorArgs represents arguments for the calculator tool.
type CalculatorArgs struct {
	Expression string `json:"expression"`
}

// NewCalculatorTool creates the calculator tool.
func NewCalculatorTool() RegisteredTool {
	return RegisteredTool{
		Meta: ToolMeta{
			Name:             "calculator",
			Description:      "Evaluate a mathematical expression (e.g. '23 * 19', '100 / 4 + 3').",
			RequiredPerm:     "calculator",
			RiskLevel:        RiskLevelLow,
			RequiresApproval: false,
			ParamSchema: `{
				"type": "object",
				"properties": {
					"expression": {"type": "string", "description": "The math expression to evaluate"}
				},
				"required": ["expression"]
			}`,
		},
		Fn: executeCalculator,
	}
}

func executeCalculator(_ *auth.ToolIdentity, arguments string) ToolResult {
	var args CalculatorArgs
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return BusinessErrorResult("calculator", "invalid arguments: "+err.Error())
	}

	result, err := evalSimpleExpression(args.Expression)
	if err != nil {
		return BusinessErrorResult("calculator", err.Error())
	}

	return SuccessResult("calculator",
		fmt.Sprintf("%s = %v", args.Expression, result),
		map[string]any{"expression": args.Expression, "result": result},
	)
}

// evalSimpleExpression evaluates simple arithmetic expressions with +, -, *, /.
// This is a basic implementation for demo purposes.
func evalSimpleExpression(expr string) (float64, error) {
	expr = strings.TrimSpace(expr)

	// Handle multiplication and division first
	parts := strings.Split(expr, "*")
	if len(parts) == 2 {
		a, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
		if err != nil {
			return 0, fmt.Errorf("invalid number: %s", parts[0])
		}
		b, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if err != nil {
			return 0, fmt.Errorf("invalid number: %s", parts[1])
		}
		return a * b, nil
	}

	parts = strings.Split(expr, "/")
	if len(parts) == 2 {
		a, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
		if err != nil {
			return 0, fmt.Errorf("invalid number: %s", parts[0])
		}
		b, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if err != nil {
			return 0, fmt.Errorf("invalid number: %s", parts[1])
		}
		if b == 0 {
			return 0, fmt.Errorf("division by zero")
		}
		return a / b, nil
	}

	parts = strings.Split(expr, "+")
	if len(parts) == 2 {
		a, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
		if err != nil {
			return 0, fmt.Errorf("invalid number: %s", parts[0])
		}
		b, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if err != nil {
			return 0, fmt.Errorf("invalid number: %s", parts[1])
		}
		return a + b, nil
	}

	parts = strings.SplitN(expr, "-", 2)
	if len(parts) == 2 && strings.TrimSpace(parts[0]) != "" {
		a, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
		if err != nil {
			return 0, fmt.Errorf("invalid number: %s", parts[0])
		}
		b, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if err != nil {
			return 0, fmt.Errorf("invalid number: %s", parts[1])
		}
		return a - b, nil
	}

	// Try parsing as a single number
	v, err := strconv.ParseFloat(expr, 64)
	if err != nil {
		return 0, fmt.Errorf("unsupported expression: %s", expr)
	}
	return v, nil
}
