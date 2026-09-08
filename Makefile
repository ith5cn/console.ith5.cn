.PHONY: db-up db-down migrate test build fmt

db-up:      ## 启动本地 Postgres
	docker compose up -d

db-down:    ## 停止并保留数据
	docker compose down

db-reset:   ## 销毁数据重来
	docker compose down -v && docker compose up -d

migrate:
	ITH5_DATABASE_URL?=postgres://ith5:ith5@localhost:55432/ith5?sslmode=disable \
	go run ./cmd/ith5-server -migrate

test:
	go test ./... -cover

fmt:
	gofmt -w .
	go vet ./...

build:
	go build -o bin/ith5-server ./cmd/ith5-server
	go build -o bin/ith5 ./cmd/ith5
	go build -o bin/ith5-hook ./cmd/ith5-hook

perf:       ## hook 延迟回归（p95 < 50ms 是硬指标）
	go test ./internal/hook/ -run Latency -v

web-build:  ## 构建管理后台（产物会被编译进 ith5-server）
	cd web && npm install && npm run build

web-dev:    ## 前端开发模式，API 代理到 localhost:8080
	cd web && npm run dev
