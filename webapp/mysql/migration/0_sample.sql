-- このファイルに記述されたSQLコマンドが、マイグレーション時に実行されます。

CREATE INDEX idx_products_name ON products(name);
ALTER TABLE products
ADD FULLTEXT INDEX ft_products_name_desc (name, description)  WITH PARSER ngram;
