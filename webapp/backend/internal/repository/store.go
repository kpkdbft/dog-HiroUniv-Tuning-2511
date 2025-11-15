package repository

import (
	"context"

	"github.com/go-redis/redis/v8"
	"github.com/jmoiron/sqlx"
)

type Store struct {
	db  DBTX
	rdb *redis.Client

	UserRepo    *UserRepository
	SessionRepo *SessionRepository
	ProductRepo *ProductRepository
	OrderRepo   *OrderRepository
}

func NewStore(db DBTX, rdb *redis.Client) *Store {
	return &Store{
		db:          db,
		rdb:         rdb,
		UserRepo:    NewUserRepository(db),
		SessionRepo: NewSessionRepository(db),
		ProductRepo: NewProductRepository(db, rdb),
		OrderRepo:   NewOrderRepository(db),
	}
}

func (s *Store) ExecTx(ctx context.Context, fn func(txStore *Store) error) error {
	db, ok := s.db.(*sqlx.DB)
	if !ok {
		return fn(s)
	}

	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	txStore := NewStore(tx, s.rdb)
	if err := fn(txStore); err != nil {
		return err
	}

	return tx.Commit()
}
