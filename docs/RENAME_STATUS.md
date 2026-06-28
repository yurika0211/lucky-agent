# 项目重命名状态 (LuckyHarness → LuckyAgent)

## 已完成

### ✅ 配置文件
- `.env.example` - 所有路径和镜像名
- `.gitignore` - 目录名

### ✅ Proto定义
- `api/proto/luckyharness.proto` → `api/proto/luckyagent.proto`
- package名、go_package、服务名已更新

### ✅ Go代码 (全部)
- 所有 package 声明
- 所有路径引用 (`.luckyharness` → `.luckyagent`)
- 数据库文件名 (`luckyharness.db` → `luckyagent.db`)
- User-Agent 字符串
- 注释和文档

### ✅ 脚本文件
- Docker脚本 (`docker/`)
- Hooks脚本 (`hooks/`)
- 安装脚本 (`scripts/`)

### ✅ 前端代码
- `README.html` - 所有品牌名称、路径、URL
- `UI/GUI/src/App.tsx` - 所有显示名称
- `UI/GUI/index.html` - 标题
- `UI/TUI/src/tui-app.tsx` - 所有显示名称和帮助文本

## 需要重新生成

### ⚠️ Proto生成文件
以下文件包含旧的类型名（`LuckyHarnessService`等），需要重新生成：
- `api/grpc/luckyagent.pb.go`
- `api/grpc/luckyagent.pb.gw.go`
- `api/grpc/luckyagent_grpc.pb.go`
- `api/grpc/server.go` (使用了proto生成的类型)

**重新生成步骤：**
```bash
# 安装依赖（如果未安装）
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@latest

# 运行生成脚本
cd api/proto
./generate.sh
```

## 验证

完成proto重新生成后，运行以下命令验证：

```bash
# 检查是否还有遗留的luckyharness引用
grep -r "luckyharness\|LuckyHarness" \
  --exclude-dir=.git \
  --exclude-dir=node_modules \
  --exclude-dir=vendor \
  --exclude="*.pb.go" \
  . | wc -l

# 应该返回 0

# 编译测试
go build ./cmd/la

# 运行测试
go test ./...
```

## 注意事项

1. **目录名称**：用户的 `~/.luckyharness` 目录会自动迁移为 `~/.luckyagent`（根据代码逻辑）
2. **Docker镜像**：新镜像名应为 `ghcr.io/yurika0211/luckyagent:latest`
3. **环境变量**：所有 `LH_*` 前缀的环境变量保持不变（LH = Lucky Harness/Agent 缩写）
4. **二进制名称**：`lh` 命令名保持不变
