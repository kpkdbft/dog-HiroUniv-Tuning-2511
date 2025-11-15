package service

import (
	"backend/internal/model"
	"backend/internal/repository"
	"backend/internal/service/utils"
	"context"
	"log"
)

type RobotService struct {
	store *repository.Store
}

func NewRobotService(store *repository.Store) *RobotService {
	return &RobotService{store: store}
}

// 注意：このメソッドは、現在、ordersテーブルのshipped_statusが"shipping"になっている注文"全件"を対象に配送計画を立てます。
// 注文の取得件数を制限した場合、ペナルティの対象になります。
func (s *RobotService) GenerateDeliveryPlan(ctx context.Context, robotID string, capacity int) (*model.DeliveryPlan, error) {
	var plan model.DeliveryPlan

	err := utils.WithTimeout(ctx, func(ctx context.Context) error {
		return s.store.ExecTx(ctx, func(txStore *repository.Store) error {
			orders, err := txStore.OrderRepo.GetShippingOrders(ctx)
			if err != nil {
				return err
			}
			plan, err = selectOrdersForDelivery(ctx, orders, robotID, capacity)
			if err != nil {
				return err
			}
			if len(plan.Orders) > 0 {
				orderIDs := make([]int64, len(plan.Orders))
				for i, order := range plan.Orders {
					orderIDs[i] = order.OrderID
				}

				if err := txStore.OrderRepo.UpdateStatuses(ctx, orderIDs, "delivering"); err != nil {
					return err
				}
				log.Printf("Updated status to 'delivering' for %d orders", len(orderIDs))
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return &plan, nil
}

func (s *RobotService) UpdateOrderStatus(ctx context.Context, orderID int64, newStatus string) error {
	return utils.WithTimeout(ctx, func(ctx context.Context) error {
		return s.store.OrderRepo.UpdateStatuses(ctx, []int64{orderID}, newStatus)
	})
}

// selectOrdersForDelivery は動的計画法を使用してナップサック問題を解きます
// 📌 高速化: 空間計算量をO(capacity)に最適化し、copy()オーバーヘッドを排除
func selectOrdersForDelivery(ctx context.Context, orders []model.Order, robotID string, robotCapacity int) (model.DeliveryPlan, error) {
	n := len(orders)
	if n == 0 {
		return model.DeliveryPlan{
			RobotID:     robotID,
			TotalWeight: 0,
			TotalValue:  0,
			Orders:      []model.Order{},
		}, nil
	}

	// コンテキストキャンセレーションチェック用
	checkEvery := 1000

	// 📌 修正点 1: DPテーブルを1次元配列に変更
	// dp[w] = 現在の注文まで見た時、重量w以下での最大価値
	dp := make([]int, robotCapacity+1)

	// 復元用: choice[i][w] = i番目の注文まで見た時、重量wでi番目の注文を選んだかどうか
	choice := make([][]bool, n)
	for i := range choice {
		choice[i] = make([]bool, robotCapacity+1)
	}

	// 動的計画法のメインループ
	for i := 0; i < n; i++ {
		// 定期的にコンテキストキャンセレーションをチェック
		if i > 0 && i%checkEvery == 0 {
			select {
			case <-ctx.Done():
				return model.DeliveryPlan{}, ctx.Err()
			default:
			}
		}

		order := orders[i]
		weight := order.Weight
		value := order.Value

		// 📌 修正点 3: copy(dp[curr], dp[prev]) を削除

		// 📌 修正点 4: ループを逆順（w := robotCapacity から）に変更
		// これにより、1次元配列でも各注文が1回しか使われないことが保証される
		for w := robotCapacity; w >= weight; w-- {
			// 現在の注文を含めた場合の価値
			// 📌 修正点 5: dp[prev][w-weight] を dp[w-weight] に変更
			newValue := dp[w-weight] + value

			// 📌 修正点 6: dp[curr][w] を dp[w] に変更
			if newValue > dp[w] {
				dp[w] = newValue
				choice[i][w] = true
			}
		}
	}
	// 最適解を復元
	bestValue := dp[robotCapacity]
	bestSet := make([]model.Order, 0)

	// 逆順に復元
	w := robotCapacity
	for i := n - 1; i >= 0; i-- {
		if w < 0 {
			break
		}
		// この注文が選ばれているかチェック
		if w >= orders[i].Weight && choice[i][w] {
			bestSet = append(bestSet, orders[i])
			w -= orders[i].Weight
		}
	}

	// 順序を元に戻す（元のordersの順序に合わせる）
	for i := 0; i < len(bestSet)/2; i++ {
		bestSet[i], bestSet[len(bestSet)-1-i] = bestSet[len(bestSet)-1-i], bestSet[i]
	}

	var totalWeight int
	for _, o := range bestSet {
		totalWeight += o.Weight
	}

	return model.DeliveryPlan{
		RobotID:     robotID,
		TotalWeight: totalWeight,
		TotalValue:  bestValue,
		Orders:      bestSet,
	}, nil
}
