package repository

import (
	"backend/internal/model"
	"context"
	"fmt"
	"strconv" // ORDER BY のために必要
	"time"

	"github.com/go-redis/redis/v8"
)

type ProductRepository struct {
	db  DBTX
	rdb *redis.Client
}

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
		whereClause = " WHERE MATCH(name, description) AGAINST (? IN BOOLEAN MODE) "
		searchPattern := req.Search
		args = append(args, searchPattern)
		countArgs = append(countArgs, searchPattern)
	}

	var total int
	var err error

	const totalCacheKey = "product:count:total"

	// 検索条件がない場合のみキャッシュを試みる
	if req.Search == "" {
		val, redisErr := r.rdb.Get(ctx, totalCacheKey).Result()
		if redisErr == nil {
			// キャッシュヒット
			total, err = strconv.Atoi(val)
			if err != nil {
				// キャッシュデータが不正な場合、フォールバック (total=0のまま)
				total = 0
			}
			// fmt.Printf("Cache Hit: Total=%d", total)
		}
		// else: キャッシュミス (total=0のまま)
	}

	// キャッシュから取得できなかった場合 (total=0) のみDBに聞く
	if total == 0 {
		err = r.db.GetContext(ctx, &total, r.db.Rebind(countQuery+whereClause), countArgs...)
		if err != nil {
			return nil, 0, err
		}

		// DBから取得し、それがキャッシュ対象（検索なし）ならRedisに保存
		if req.Search == "" {
			// Setのエラーは非クリティカルなので無視
			r.rdb.Set(ctx, totalCacheKey, total, 5*time.Minute)
		}
	}

	var products []model.Product

	finalQuery := baseQuery + whereClause + " "
	finalQuery += " ORDER BY " + req.SortField + " " + req.SortOrder + " , product_id ASC"

	if req.PageSize > 0 {
		finalQuery += " LIMIT ? OFFSET ? "
		args = append(args, req.PageSize, req.Offset)
	}

	err = r.db.SelectContext(ctx, &products, r.db.Rebind(finalQuery), args...)
	if err != nil {
		return nil, 0, err
	}

	return products, total, nil
}
