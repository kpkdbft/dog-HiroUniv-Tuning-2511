-- -- このファイルに記述されたSQLコマンドが、マイグレーション時に実行されます。

-- 名前検索用のインデックス
CREATE INDEX idx_products_name ON products(name);

-- COUNT(*) を高速化するためのセカンダリインデックス
CREATE INDEX idx_products_count ON products(product_id);

-- 配送中一覧を高速化（GetShippingOrders 用）
ALTER TABLE orders
  ADD INDEX idx_orders_shipping (shipped_status, product_id, order_id);

ALTER TABLE products
  ADD FULLTEXT INDEX ft_products_name_desc (name, description) WITH PARSER ngram;
