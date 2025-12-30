# 项目名称
BINARY_NAME=vc
# 主文件路径
MAIN_PATH=cmd/video-compress/main.go
# 编译输出目录
BUILD_DIR=bin

.PHONY: all build clean run install

all: build

# 编译针对当前系统的二进制文件
build:
	@echo "Building..."
	@mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)
	@echo "Build complete: $(BUILD_DIR)/$(BINARY_NAME)"

# 编译针对 MacOS Arm64 (Apple Silicon) 的优化版本
build-mac:
	@echo "Building for Apple Silicon..."
	@mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)
	@echo "Build complete."

# 运行程序 (测试用)
run:
	go run $(MAIN_PATH)

# 清理构建产物
clean:
	@echo "Cleaning..."
	@rm -rf $(BUILD_DIR)
	@go clean
	@echo "Clean complete."

# 安装到系统路径 (需 sudo 或确保 ~/bin 在 PATH 中)
install: build-mac
# 	@echo "Installing to /usr/local/bin..."
# 	@mv $(BUILD_DIR)/$(BINARY_NAME) /usr/local/bin/
	@echo "Installing to ~/bin..."
	@mv $(BUILD_DIR)/$(BINARY_NAME) ~/bin/
	@echo "Installation complete."

# 整理依赖
deps:
	@go mod tidy
	@go mod verify