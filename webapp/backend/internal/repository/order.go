package repository

import (
	"backend/internal/model"
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"
)

type OrderRepository struct {
	db DBTX
}

func NewOrderRepository(db DBTX) *OrderRepository {
	return &OrderRepository{db: db}
}

// 注文を作成し、生成された注文IDを返す
func (r *OrderRepository) Create(ctx context.Context, order *model.Order) (string, error) {
	query := `INSERT INTO orders (user_id, product_id, shipped_status, created_at) VALUES (?, ?, 'shipping', NOW())`
	result, err := r.db.ExecContext(ctx, query, order.UserID, order.ProductID)
	if err != nil {
		return "", err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d", id), nil
}

// 複数の注文を一括で作成し、生成された注文IDのリストを返す
func (r *OrderRepository) CreateBulk(ctx context.Context, orders []model.Order) ([]string, error) {
	if len(orders) == 0 {
		return []string{}, nil
	}

	// VALUES句を構築
	query := `INSERT INTO orders (user_id, product_id, shipped_status, created_at) VALUES `
	args := make([]interface{}, 0, len(orders)*2)
	placeholders := make([]string, 0, len(orders))

	for _, order := range orders {
		placeholders = append(placeholders, "(?, ?, 'shipping', NOW())")
		args = append(args, order.UserID, order.ProductID)
	}

	query += strings.Join(placeholders, ", ")

	// Bulk insertを実行
	result, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to bulk insert orders: %w", err)
	}

	// 最初に挿入されたIDを取得
	firstID, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("failed to get last insert id: %w", err)
	}

	// 挿入された行数を取得
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("failed to get rows affected: %w", err)
	}

	// 生成されたIDのリストを作成（AUTO_INCREMENTは連続したIDを生成することを前提）
	orderIDs := make([]string, 0, rowsAffected)
	for i := int64(0); i < rowsAffected; i++ {
		orderIDs = append(orderIDs, fmt.Sprintf("%d", firstID+i))
	}

	return orderIDs, nil
}

// 複数の注文IDのステータスを一括で更新
// 主に配送ロボットが注文を引き受けた際に一括更新をするために使用
func (r *OrderRepository) UpdateStatuses(ctx context.Context, orderIDs []int64, newStatus string) error {
	if len(orderIDs) == 0 {
		return nil
	}
	query, args, err := sqlx.In("UPDATE orders SET shipped_status = ? WHERE order_id IN (?)", newStatus, orderIDs)
	if err != nil {
		return err
	}
	query = r.db.Rebind(query)
	_, err = r.db.ExecContext(ctx, query, args...)
	return err
}

// 配送中(shipped_status:shipping)の注文一覧を取得
func (r *OrderRepository) GetShippingOrders(ctx context.Context) ([]model.Order, error) {
	var orders []model.Order
	query := `
        SELECT
            o.order_id,
            p.weight,
            p.value
        FROM orders o
        JOIN products p ON o.product_id = p.product_id
        WHERE o.shipped_status = 'shipping'
    `
	err := r.db.SelectContext(ctx, &orders, query)
	return orders, err
}

// 注文履歴一覧を取得 (DB側でソート、フィルタ、Offset/Limitを実行)
func (r *OrderRepository) ListOrders(ctx context.Context, userID int, req model.ListRequest) ([]model.Order, int, error) {

	// --- 1. ソート順の決定 ---
	// SQLインジェクション防止のため、ソート可能な列をホワイトリストで管理
	sortFieldMap := map[string]string{
		"order_id":       "o.order_id",
		"product_name":   "p.name",
		"created_at":     "o.created_at",
		"shipped_status": "o.shipped_status",
		"arrived_at":     "o.arrived_at",
	}

	sortColumn, ok := sortFieldMap[req.SortField]
	if !ok {
		sortColumn = "o.order_id" // デフォルトのソート列
	}

	sortOrder := "ASC"
	if strings.ToUpper(req.SortOrder) == "DESC" {
		sortOrder = "DESC"
	}

	if req.PageSize < 0 {
		// PageSizeが0以下の場合、デフォルト値を設定（例: 20件）
		// これにより LIMIT 0 を防ぐ
		req.PageSize = 20
	}
	if req.Offset < 0 {
		req.Offset = 0 // 負のオフセットは0にする
	}
	// --- 2. フィルタリング条件の構築 (総件数クエリとメインクエリで共用) ---
	whereClauses := []string{"o.user_id = ?"}
	// メインクエリ用の引数リスト
	args := []interface{}{userID}
	// COUNTクエリ用の引数リスト (LIMIT/OFFSETを含まないため別管理)
	countArgs := []interface{}{userID}

	if req.Search != "" {
		var searchPattern string
		if req.Type == "prefix" {
			searchPattern = req.Search + "%"
		} else {
			// デフォルトは "contains"
			searchPattern = "%" + req.Search + "%"
		}
		whereClauses = append(whereClauses, "p.name LIKE ?")
		args = append(args, searchPattern)
		countArgs = append(countArgs, searchPattern)
	}

	whereQuery := strings.Join(whereClauses, " AND ")

	// --- 3. 総件数(total)の取得クエリ (フィルタ条件を適用) ---
	countQuery := `
		SELECT COUNT(*)
		FROM orders o
		JOIN products p ON o.product_id = p.product_id
		WHERE ` + whereQuery

	var total int
	countQueryRebound := r.db.Rebind(countQuery)
	// COUNTクエリには countArgs を使用
	if err := r.db.GetContext(ctx, &total, countQueryRebound, countArgs...); err != nil {
		return nil, 0, fmt.Errorf("failed to count orders: %w", err)
	}

	// --- 4. メインクエリの構築 (ORDER BY, LIMIT, OFFSET) ---

	var orderByClause string
	if sortColumn == "o.order_id" {
		orderByClause = fmt.Sprintf("ORDER BY %s %s", sortColumn, sortOrder)
	} else {
		orderByClause = fmt.Sprintf("ORDER BY %s %s, o.order_id ASC", sortColumn, sortOrder)
	}

	query := `
		SELECT 
			o.order_id, 
			o.product_id, 
			o.shipped_status, 
			o.created_at, 
			o.arrived_at,
			p.name AS product_name
		FROM orders o
		JOIN products p ON o.product_id = p.product_id
		WHERE ` + whereQuery + `
		` + orderByClause + `
		LIMIT ? OFFSET ?`

	// メインクエリ用の引数にLIMITとOFFSETを追加
	args = append(args, req.PageSize, req.Offset)

	// --- 5. クエリ実行とマッピング ---
	// ( ... これ以降のロジックは変更なし )
	// --- 5. クエリ実行とマッピング ---
	type orderRow struct {
		OrderID       int          `db:"order_id"`
		ProductID     int          `db:"product_id"`
		ShippedStatus string       `db:"shipped_status"`
		CreatedAt     sql.NullTime `db:"created_at"`
		ArrivedAt     sql.NullTime `db:"arrived_at"`
		ProductName   string       `db:"product_name"`
	}

	var ordersRaw []orderRow
	queryRebound := r.db.Rebind(query)
	// メインクエリには args を使用
	if err := r.db.SelectContext(ctx, &ordersRaw, queryRebound, args...); err != nil {
		return nil, 0, fmt.Errorf("failed to list orders: %w", err)
	}

	// --- 6. 結果のマッピング ---
	// メモリ上でのフィルタリングやソート、スライス操作はすべて不要
	orders := make([]model.Order, 0, len(ordersRaw))
	for _, o := range ordersRaw {
		orders = append(orders, model.Order{
			OrderID:       int64(o.OrderID),
			ProductID:     o.ProductID,
			ProductName:   o.ProductName,
			ShippedStatus: o.ShippedStatus,
			CreatedAt:     o.CreatedAt.Time, // NullTimeからTimeへ
			ArrivedAt:     o.ArrivedAt,
		})
	}

	// DBから取得した件数(pagedOrders)と、フィルタ条件に合う総件数(total)を返す
	return orders, total, nil
}
