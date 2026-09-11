.PHONY: e2e db-up db-down db-reset db-testdb migrate seed test test-db build fmt web-build web-dev

DSN      ?= postgres://ith5:ith5@localhost:55432/ith5?sslmode=disable
# 集成测试每次都会回滚并重建全部结构，必须指向专用的测试库
TEST_DSN ?= postgres://ith5:ith5@localhost:55432/ith5_test?sslmode=disable

db-up:      ## 启动本地 Postgres
	docker compose up -d

db-down:    ## 停止并保留数据
	docker compose down

db-reset:   ## 销毁数据重来（开发期结构直接改 00001_init.sql，改完跑这个）
	docker compose down -v && docker compose up -d

migrate:    ## 执行迁移
	ITH5_DATABASE_URL='$(DSN)' go run ./cmd/ith5-server -migrate

seed:       ## 写入演示数据
	ITH5_DATABASE_URL='$(DSN)' go run ./cmd/ith5-server -seed

test:       ## 单元测试（不需要数据库）
	go test ./internal/... ./cmd/... -cover

db-testdb:  ## 创建集成测试用的独立数据库（幂等）
	docker exec ith5-postgres psql -U ith5 -tc "SELECT 1 FROM pg_database WHERE datname='ith5_test'" | grep -q 1 \
		|| docker exec ith5-postgres psql -U ith5 -c "CREATE DATABASE ith5_test"

test-db: db-testdb  ## 全部测试，含数据库集成测试
	ITH5_TEST_DATABASE_URL='$(TEST_DSN)' go test -p 1 ./internal/... ./cmd/... -cover

fmt:
	gofmt -w .
	go vet ./internal/... ./cmd/...

build:      ## 服务端与员工侧物化工具
	go build -o bin/ith5-server ./cmd/ith5-server
	go build -o bin/ith5-materialize ./cmd/ith5-materialize

e2e:        ## 端到端（需要 docker 里的 Postgres 与 node 20；见 scripts/e2e.sh）
	bash scripts/e2e.sh

web-build:  ## 构建管理后台（产物会被编译进 ith5-server）
	cd web && npm install && npm run build

web-dev:    ## 前端开发模式，API 代理到 localhost:8080
	cd web && npm run dev
