package tools

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/example/agent-eino-demo/internal/auth"
)

// OrderArgs represents arguments for order tools.
type OrderArgs struct {
	OrderID string `json:"order_id"`
}

// Order represents a sample order.
type Order struct {
	ID     string `json:"id"`
	UserID string `json:"user_id"`
	Status string `json:"status"`
	Amount string `json:"amount"`
	Desc   string `json:"description"`
}

// OrderRepository is the durable business boundary used by order tools.
type OrderRepository interface {
	QueryByUserContext(ctx context.Context, userID string) ([]Order, error)
	DeleteContext(ctx context.Context, userID, orderID, idempotencyKey string) (deleted, replayed bool, err error)
}

// OrderStore holds sample orders in memory, keyed by userID+orderID.
type OrderStore struct {
	mu      sync.RWMutex
	orders  map[string][]Order // userID -> orders
	effects map[string]bool    // idempotency key -> original delete result
}

// NewOrderStore creates an OrderStore with seed data.
func NewOrderStore() *OrderStore {
	s := &OrderStore{
		orders:  make(map[string][]Order),
		effects: make(map[string]bool),
	}
	s.seed()
	return s
}

func (s *OrderStore) seed() {
	for _, order := range DefaultOrders() {
		s.orders[order.UserID] = append(s.orders[order.UserID], order)
	}
}

// DefaultOrders returns the demo seed data used by memory and PostgreSQL.
func DefaultOrders() []Order {
	return []Order{
		{ID: "A-1001", UserID: "u_admin", Status: "pending", Amount: "¥199.00", Desc: "无线鼠标"},
		{ID: "A-1002", UserID: "u_admin", Status: "shipped", Amount: "¥599.00", Desc: "机械键盘"},
		{ID: "A-1003", UserID: "u_admin", Status: "pending", Amount: "¥4,999.00", Desc: "27寸4K显示器"},
		{ID: "A-1004", UserID: "u_admin", Status: "delivered", Amount: "¥899.00", Desc: "降噪耳机"},
		{ID: "A-1005", UserID: "u_admin", Status: "pending", Amount: "¥12,599.00", Desc: "MacBook Pro M4 维修服务"},
		{ID: "A-1006", UserID: "u_admin", Status: "cancelled", Amount: "¥299.00", Desc: "手机壳（已取消）"},
		{ID: "A-1007", UserID: "u_admin", Status: "pending", Amount: "¥6,499.00", Desc: "年度云服务器续费"},
		{ID: "A-1008", UserID: "u_admin", Status: "shipped", Amount: "¥1,299.00", Desc: "人体工学椅"},
		{ID: "B-2001", UserID: "u_visitor", Status: "pending", Amount: "¥49.00", Desc: "USB 数据线"},
		{ID: "B-2002", UserID: "u_visitor", Status: "delivered", Amount: "¥299.00", Desc: "蓝牙耳机"},
		{ID: "B-2003", UserID: "u_visitor", Status: "pending", Amount: "¥2,399.00", Desc: "平板电脑"},
		{ID: "B-2004", UserID: "u_visitor", Status: "shipped", Amount: "¥159.00", Desc: "移动电源"},
		{ID: "B-2005", UserID: "u_visitor", Status: "pending", Amount: "¥8,999.00", Desc: "年度会员套餐"},
	}
}

// QueryByUser returns orders for a given user.
func (s *OrderStore) QueryByUser(userID string) []Order {
	orders, _ := s.QueryByUserContext(context.Background(), userID)
	return orders
}

func (s *OrderStore) QueryByUserContext(ctx context.Context, userID string) ([]Order, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := auth.CheckUserScope(ctx, userID); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Order(nil), s.orders[userID]...), nil
}

// Delete removes an order for a user.
func (s *OrderStore) Delete(userID, orderID string) bool {
	deleted, _, _ := s.DeleteContext(context.Background(), userID, orderID, "")
	return deleted
}

func (s *OrderStore) DeleteContext(ctx context.Context, userID, orderID, idempotencyKey string) (bool, bool, error) {
	if err := ctx.Err(); err != nil {
		return false, false, err
	}
	if err := auth.CheckUserScope(ctx, userID); err != nil {
		return false, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if idempotencyKey != "" {
		if deleted, ok := s.effects[idempotencyKey]; ok {
			return deleted, true, nil
		}
	}

	orders, ok := s.orders[userID]
	if !ok {
		if idempotencyKey != "" {
			s.effects[idempotencyKey] = false
		}
		return false, false, nil
	}
	for i, o := range orders {
		if o.ID == orderID {
			s.orders[userID] = append(orders[:i], orders[i+1:]...)
			if idempotencyKey != "" {
				s.effects[idempotencyKey] = true
			}
			return true, false, nil
		}
	}
	if idempotencyKey != "" {
		s.effects[idempotencyKey] = false
	}
	return false, false, nil
}

// NewQueryOrderTool creates the query_order tool.
func NewQueryOrderTool(store OrderRepository) RegisteredTool {
	return RegisteredTool{
		Meta: ToolMeta{
			Name:             "query_order",
			Description:      "Query orders for the current user. Returns the user's own orders only.",
			RequiredPerm:     "query_order",
			RiskLevel:        RiskLevelLow,
			RequiresApproval: false,
			ParamSchema: `{
				"type": "object",
				"properties": {
					"order_id": {"type": "string", "description": "Optional specific order ID"}
				}
			}`,
		},
		Fn: func(identity *auth.ToolIdentity, arguments string) ToolResult {
			if identity == nil || identity.UserID == "" {
				return SystemErrorResult("query_order", "missing authenticated user identity", "")
			}
			userID := identity.UserID

			var args OrderArgs
			json.Unmarshal([]byte(arguments), &args)

			orders, err := store.QueryByUserContext(toolContext(identity), userID)
			if err != nil {
				return SystemErrorResult("query_order", err.Error(), identity.RunID)
			}

			if args.OrderID != "" {
				var found *Order
				for i := range orders {
					if orders[i].ID == args.OrderID {
						found = &orders[i]
						break
					}
				}
				if found == nil {
					return BusinessErrorResult("query_order",
						fmt.Sprintf("订单 %s 不存在或不在当前用户名下", args.OrderID))
				}
				data, _ := json.Marshal(found)
				return SuccessResult("query_order", string(data),
					map[string]any{"order_id": args.OrderID})
			}

			return SuccessResult("query_order", formatOrderList(orders),
				map[string]any{"order_count": len(orders)})
		},
	}
}

// NewDeleteOrderTool creates the delete_order tool (admin, requires approval).
func NewDeleteOrderTool(store OrderRepository) RegisteredTool {
	return RegisteredTool{
		Meta: ToolMeta{
			Name:             "delete_order",
			Description:      "Delete an order by order_id. This is a high-risk operation that will trigger an automatic human approval flow before execution — you MUST call this tool directly, do NOT ask the user for confirmation.",
			RequiredPerm:     "delete_order",
			RiskLevel:        RiskLevelHigh,
			RequiresApproval: true,
			ParamSchema: `{
				"type": "object",
				"properties": {
					"order_id": {"type": "string", "description": "The order ID to delete"}
				},
				"required": ["order_id"]
			}`,
		},
		Fn: func(identity *auth.ToolIdentity, arguments string) ToolResult {
			if identity == nil || identity.UserID == "" {
				return SystemErrorResult("delete_order", "missing authenticated user identity", "")
			}
			userID := identity.UserID

			var args OrderArgs
			if err := json.Unmarshal([]byte(arguments), &args); err != nil {
				return BusinessErrorResult("delete_order", "invalid arguments: "+err.Error())
			}

			if args.OrderID == "" {
				return BusinessErrorResult("delete_order", "order_id is required")
			}

			deleted, replayed, err := store.DeleteContext(toolContext(identity), userID, args.OrderID,
				toolIdempotencyKey(identity, "delete_order", arguments))
			if err != nil {
				return SystemErrorResult("delete_order", err.Error(), identity.RunID)
			}
			if !deleted {
				return BusinessErrorResult("delete_order",
					fmt.Sprintf("order %s not found for current user", args.OrderID))
			}

			return SuccessResult("delete_order",
				fmt.Sprintf("⚠️ 订单 %s 已删除", args.OrderID),
				map[string]any{"order_id": args.OrderID, "idempotent_replay": replayed})
		},
	}
}

func toolContext(identity *auth.ToolIdentity) context.Context {
	if identity != nil && identity.Context != nil {
		return identity.Context
	}
	return context.Background()
}

func toolIdempotencyKey(identity *auth.ToolIdentity, toolName, arguments string) string {
	if identity == nil || identity.UserID == "" || identity.RunID == "" {
		return ""
	}
	callID := identity.ToolCallID
	if callID == "" {
		digest := sha256.Sum256([]byte(arguments))
		callID = fmt.Sprintf("args:%x", digest[:12])
	}
	return identity.UserID + ":" + identity.RunID + ":" + toolName + ":" + callID
}

var _ OrderRepository = (*OrderStore)(nil)

// formatOrderList formats an order list as a readable text table.
func formatOrderList(orders []Order) string {
	if len(orders) == 0 {
		return "当前用户暂无订单"
	}

	headers := []string{"订单号", "状态", "金额", "商品描述"}
	rows := make([][]string, 0, len(orders))
	for _, o := range orders {
		rows = append(rows, []string{o.ID, formatStatus(o.Status), o.Amount, o.Desc})
	}

	// The widths are measured from the content, and the rules are drawn from the
	// same numbers, so the borders line up by construction rather than by hand.
	// The previous version padded with %-8s and hand-wrote the rules: the borders
	// came out 58 columns, the header 60 and the data rows 61, because fmt pads by
	// bytes while a CJK rune is three bytes and two columns.
	widths := columnWidths(headers, rows)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📋 共 %d 笔订单：\n", len(orders)))
	sb.WriteString(renderTableBorder(widths, "┌", "┬", "┐"))
	sb.WriteString("\n")
	sb.WriteString(renderTableRow(headers, widths, true))
	sb.WriteString("\n")
	sb.WriteString(renderTableBorder(widths, "├", "┼", "┤"))
	sb.WriteString("\n")
	for _, row := range rows {
		sb.WriteString(renderTableRow(row, widths, false))
		sb.WriteString("\n")
	}
	sb.WriteString(renderTableBorder(widths, "└", "┴", "┘"))
	sb.WriteString("\n")
	sb.WriteString("\n💡 提示：删除订单为高危操作，需要管理员审批。高金额订单请谨慎操作。")
	return sb.String()
}

// formatOrderDetail formats a single order detail.
func formatOrderDetail(o Order) string {
	status := formatStatus(o.Status)
	return fmt.Sprintf("📦 订单详情\n──────────────\n订单号：%s\n状态：%s\n金额：%s\n商品：%s", o.ID, status, o.Amount, o.Desc)
}

// formatStatus translates order status to Chinese with emoji.
func formatStatus(status string) string {
	switch status {
	case "pending":
		return "🟡待处理"
	case "shipped":
		return "🔵已发货"
	case "delivered":
		return "🟢已送达"
	case "cancelled":
		return "🔴已取消"
	default:
		return "❓" + status
	}
}
