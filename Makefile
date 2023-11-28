.PHONY: mock
mock:
	@mockgen -destination=D:\go\GO-LEARNING\src\redis-lock\mocks\cmdable.mock.go -package=redismocks github.com/redis/go-redis/v9 Cmdable
	@go mod tidy
