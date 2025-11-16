package repository

import (
	"backend/internal/model"
	"context"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"encoding/json"
	"golang.org/x/sync/singleflight"
)

type ProductRepository struct {
	db  DBTX
	rdb *redis.Client
}

var productSF singleflight.Group

func NewProductRepository(db DBTX, rdb *redis.Client) *ProductRepository {
	return &ProductRepository{db: db, rdb: rdb}
}

// 商品一覧を取得 (DB側でソート、フィルタ、ページネーションを実行)
func (r *ProductRepository) ListProducts(ctx context.Context, userID int, req model.ListRequest) ([]model.Product, int, error) {
	fmt.Printf("list products")

	baseQuery := `
		SELECT product_id, name, value, weight, image, description
		FROM products
	`
	countQuery := `
		SELECT COUNT(*)
		FROM products
	`

	whereClause := ""
	args := []interface{}{}
	countArgs := []interface{}{}

	if req.Search != "" {
		// LIKE 検索にフォールバック（E2Eの安定性・互換性を優先）
		whereClause = " WHERE (name LIKE ? OR description LIKE ?) "
		searchPattern := "%" + req.Search + "%"
		args = append(args, searchPattern, searchPattern)
		countArgs = append(countArgs, searchPattern, searchPattern)
	}

	var total int
	// 総件数は常にDBから取得（E2Eの安定性優先。COUNTは軽量インデックスで最適化済み）
	if err := r.db.GetContext(ctx, &total, r.db.Rebind(countQuery+whereClause), countArgs...); err != nil {
		return nil, 0, err
	}

	var products []model.Product

	finalQuery := baseQuery + whereClause + " "
	finalQuery += " ORDER BY " + req.SortField + " " + req.SortOrder + " , product_id ASC"

	if req.PageSize > 0 {
		finalQuery += " LIMIT ? OFFSET ? "
		args = append(args, req.PageSize, req.Offset)
	}

	// 一覧ページの短TTLキャッシュ
	listKey := fmt.Sprintf("product:list:q=%s:sort=%s:%s:p=%d:s=%d", req.Search, req.SortField, req.SortOrder, req.Page, req.PageSize)
	if s, errGet := r.rdb.Get(ctx, listKey).Result(); errGet == nil {
		if json.Unmarshal([]byte(s), &products) == nil {
			return products, total, nil
		}
	}
	v, errSF, _ := productSF.Do(listKey, func() (interface{}, error) {
		var tmp []model.Product
		if er := r.db.SelectContext(ctx, &tmp, r.db.Rebind(finalQuery), args...); er != nil {
			return nil, er
		}
		if b, mErr := json.Marshal(tmp); mErr == nil {
			_ = r.rdb.Set(ctx, listKey, b, 60*time.Second).Err()
		}
		return tmp, nil
	})
	if errSF != nil {
		return nil, 0, errSF
	}
	products = v.([]model.Product)

	return products, total, nil
}
