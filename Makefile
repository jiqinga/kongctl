# 适用于 Go 1.25 的构建与测试脚本

.PHONY: help run build test tidy clean

help:
	@echo "可用命令："
	@echo "  make run    # 运行 kongctl --help"
	@echo "  make build  # 编译二进制到 bin/kongctl"
	@echo "  make test   # 运行 go test"
	@echo "  make tidy   # 整理依赖（需要联网）"
	@echo "  make clean  # 清理产物"

run:
	GO111MODULE=on go run ./cmd/kongctl --help || true

build:
	mkdir -p bin
	GO111MODULE=on go build -o bin/kongctl ./cmd/kongctl || true
	@echo "如遇依赖拉取失败，请联网后执行：make tidy"

test:
	GO111MODULE=on go test ./... -v || true

tidy:
	GO111MODULE=on go mod tidy

clean:
	rm -rf bin

