package repository

import (
	"backend/internal/model"
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/go-redis/redis/v8"
	// Rdbクライアントのためにインポート
)

type ProductRepository struct {
	db  DBTX
	rdb *redis.Client
}

func NewProductRepository(db DBTX, rdb *redis.Client) *ProductRepository {
	return &ProductRepository{db: db, rdb: rdb}
}

// 商品一覧を全件取得し、アプリケーション側でページング処理を行う
func (r *ProductRepository) ListProducts(ctx context.Context, userID int, req model.ListRequest) ([]model.Product, int, error) {
	fmt.Printf("list products")
	var products []model.Product
	baseQuery := `
		SELECT product_id, name, value, weight, image, description
		FROM products
	`
	countQuery := `
		SELECT COUNT(*)
		FROM products
	`
	args := []interface{}{}
	countArgs := []interface{}{}

	if req.Search != "" {
		// 検索ありの場合: FULLTEXT検索を適用 (キャッシュ対象外)
		baseQuery += " WHERE MATCH(name, description) AGAINST (? IN BOOLEAN MODE) "
		countQuery += " WHERE MATCH(name, description) AGAINST (? IN BOOLEAN MODE) "
		searchPattern := req.Search + "*"
		args = append(args, searchPattern)
		countArgs = append(countArgs, searchPattern)
	}

	var total int
	var err error

	// 1. 🔍 COUNT(*)のキャッシュ処理
	// 検索条件がない（req.Search == ""）場合のみ、キャッシュを利用する。
	if req.Search == "" {
		const cacheKey = "product:count:total"

		// 🚨 注意: r.Rdb は ProductRepository に Rdb クライアントがDIされていることを前提
		// r.Rdb がない場合は、Store経由でアクセスするように修正が必要です。
		rdbClient := r.rdb // 仮にここでRedisクライアントにアクセス可能とします

		val, redisErr := rdbClient.Get(ctx, cacheKey).Result()

		if redisErr == nil {
			// キャッシュヒット: Redisから取得した値をセットし、DBアクセスをスキップ
			total, err = strconv.Atoi(val)
			if err == nil {
				// fmt.Printf("Cache Hit: Total=%d", total)
				goto ExecuteSelectQuery // DBのCOUNT(*)をスキップし、SELECTへジャンプ
			}
		}
		// Redisエラー (キャッシュミス) の場合、DBへフォールバック
	}

	// 2. 🗃️ DBからのCOUNT(*)実行
	err = r.db.GetContext(ctx, &total, r.db.Rebind(countQuery), countArgs...)
	fmt.Printf("%v", err)

	if err != nil {
		return nil, 0, err
	}

	// 3. 💾 キャッシュミスの場合、Redisに書き込み
	if req.Search == "" {
		const cacheKey = "product:count:total"
		rdbClient := r.rdb // Redisクライアントを取得

		// TTL (Time To Live): 5分間キャッシュ
		rdbClient.Set(ctx, cacheKey, total, 5*time.Minute)
	}

	// 4. SELECTクエリの実行
ExecuteSelectQuery: // キャッシュヒットまたはDB COUNT(*)後にここにジャンプ

	baseQuery += " ORDER BY " + req.SortField + " " + req.SortOrder + " , product_id ASC"
	if req.PageSize != 0 {
		baseQuery += " LIMIT ? "
		args = append(args, req.PageSize)
	}
	if req.Offset != 0 {
		baseQuery += " OFFSET ? "
		args = append(args, req.Offset)
	}
	err = r.db.SelectContext(ctx, &products, r.db.Rebind(baseQuery), args...) // Rebindを追記
	if err != nil {
		return nil, 0, err
	}

	return products, total, nil
}
