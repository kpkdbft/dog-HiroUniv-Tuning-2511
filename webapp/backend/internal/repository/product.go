package repository

import (
	"backend/internal/model"
	"context"
	"fmt"
)

type ProductRepository struct {
	db DBTX
}

func NewProductRepository(db DBTX) *ProductRepository {
	return &ProductRepository{db: db}
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
		baseQuery += " WHERE MATCH(name, description) AGAINST (? IN NATURAL LANGUAGE MODE) "
		countQuery += " WHERE MATCH(name, description) AGAINST (? IN NATURAL LANGUAGE MODE) "
		// searchPattern := "%" + req.Search + "%"
		args = append(args, req.Search)
		countArgs = append(countArgs, req.Search)
	}

	var total int
	err := r.db.GetContext(ctx, &total, r.db.Rebind(countQuery), countArgs...)
	fmt.Printf("%v", err)

	if err != nil {
		return nil, 0, err
	}

	baseQuery += " ORDER BY " + req.SortField + " " + req.SortOrder + " , product_id ASC"
	if req.PageSize != 0 {
		baseQuery += " LIMIT ? "
		args = append(args, req.PageSize)
	}
	if req.Offset != 0 {
		baseQuery += " OFFSET ? "
		args = append(args, req.Offset)
	}
	err = r.db.SelectContext(ctx, &products, baseQuery, args...)
	if err != nil {
		return nil, 0, err
	}

	return products, total, nil
}
