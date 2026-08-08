# Brain — 安装与使用手册

Brain 是一个独立的记忆网关。把它跑起来,然后把你的 coding CLI(OpenCode/Codex/Aider/ClaudeCode/Continue)指向它 —— 换 CLI 的时候记忆自动跟着走。

## 一、从 GitHub 获取

```bash
git clone https://github.com/damonlinyz/brain.git
cd brain
```

## 二、自动安装依赖

Brain 是一个 Go 项目,需要 Go 1.21+ 和 Postgres 16+ / pgvector。

### 2.1 装 Go(如果还没有)

```bash
# macOS
brew install go

# Linux(Ubuntu/Debian)
sudo apt install golang-go

# 验证
go version
```

### 2.2 装 Postgres + pgvector(如果还没有)

```bash
# 用 Docker(最快)
docker run -d --name brain-db -e POSTGRES_PASSWORD=brain -p 5432:5432 pgvector/pgvector:pg16

# 或者用已有的 PG 16 实例,装上 pgvector 扩展:
#   CREATE EXTENSION IF NOT EXISTS vector;
```

### 2.3 创建数据库 + 跑迁移

```bash
# 创建数据库
docker exec -it brain-db psql -U postgres -c "CREATE DATABASE brain;"

# 跑迁移(建表)
for f in migrations/04*_v2_*.sql; do
  docker exec -i brain-db psql -U postgres -d brain -v ON_ERROR_STOP=1 < $f
done
```

### 2.4 下载 Go 依赖 + 编译

```bash
go mod download
go build -o brain ./cmd/brain
```

> 如果 Go 模块下载很慢,用国内 proxy:`export GOPROXY=https://goproxy.cn,direct`

### 2.5 装 Ollama(大脑用的嵌入模型,召回记忆需要)

```bash
# macOS: brew install ollama
# Linux: curl -fsSL https://ollama.com/install.sh | sh

# 启动并拉模型
ollama serve &
ollama pull nomic-embed-text
```

## 三、配置环境变量

把下面写进你的 `~/.zshrc` / `~/.bashrc`,或者每次启动前 export:

```bash
# 数据库
export DATABASE_URL="postgres://postgres:brain@localhost:5432/brain?sslmode=disable"

# 嵌入模型(脑默认用 Ollama)
export EMBEDDING_BASE_URL="http://localhost:11434/v1"
export EMBEDDING_MODEL="nomic-embed-text"

# 上游 LLM(你的 CLI 实际用的模型——脑只是把记忆注入后原样转发给它)
export UPSTREAM_LLM_BASE_URL="https://api.deepseek.com/v1"
export UPSTREAM_LLM_API_KEY="你的-deepseek-key"   # 或者换成 Claude/OpenAI
export UPSTREAM_LLM_MODEL="deepseek-chat"

# 脑自己的配置
export BRAIN_PORT="8090"                              # 脑监听的端口
export BRAIN_USER_ID="00000000-0000-0000-0000-000000000001"  # 你的用户 ID
export BRAIN_TOKEN="你设的密码"                        # CLI 连上来用的 bearer token
```

## 四、启动

```bash
./brain serve
```

看到 `brain listening port=8090` 就说明跑起来了。

测试一下能不能通:

```bash
curl -s http://localhost:8090/health
# → 返回空 200
```

## 四（补）、记忆 API

`brain serve` 除了做 LLM 网关，还直接暴露记忆 CRUD（带 `Authorization: Bearer <BRAIN_TOKEN>`）：

```bash
# 写入一条记忆
curl -s -H "Authorization: Bearer $BRAIN_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"raw_text":"我叫 Damon，喜欢深色主题"}' \
     http://localhost:8090/api/v1/memory/ingest

# 召回（JSON，每条记忆带来源 provenance：vector/keyword/graph/core）
curl -s -H "Authorization: Bearer $BRAIN_TOKEN" \
     "http://localhost:8090/api/v1/memory/recall?query=主题偏好"

# 召回的人类可读 Markdown 视图（#1 readable layer）
curl -s -H "Authorization: Bearer $BRAIN_TOKEN" \
     "http://localhost:8090/api/v1/memory/recall?query=主题偏好&format=markdown"

# 记忆总览（Markdown）
curl -s -H "Authorization: Bearer $BRAIN_TOKEN" \
     http://localhost:8090/api/v1/memory/index

# 列出 / 查看 / 删除单条记忆
curl -s -H "Authorization: Bearer $BRAIN_TOKEN" http://localhost:8090/api/v1/memory/nodes
curl -s -H "Authorization: Bearer $BRAIN_TOKEN" http://localhost:8090/api/v1/memory/nodes/<id>
curl -X DELETE -H "Authorization: Bearer $BRAIN_TOKEN" http://localhost:8090/api/v1/memory/nodes/<id>

# 修正一条记错的记忆
curl -s -X POST -H "Authorization: Bearer $BRAIN_TOKEN" -H "Content-Type: application/json" \
     -d '{"content":"我叫 Lin","summary":"名字是 Lin","reason":"之前记错了"}' \
     http://localhost:8090/api/v1/memory/nodes/<id>/correct

# 钉住 / 取消钉住 —— pinned-core 永远注入（#2，像个人 CLAUDE.md）
curl -s -X POST -H "Authorization: Bearer $BRAIN_TOKEN" \
     http://localhost:8090/api/v1/memory/nodes/<id>/pin
curl -s -X POST -H "Authorization: Bearer $BRAIN_TOKEN" \
     http://localhost:8090/api/v1/memory/nodes/<id>/unpin
```

> **pinned-core（#2）**：被 `pin` 的记忆会标成 `tier=core`，每次召回**无视相似度分数**直接注入，放在 prompt 最前面的 `## Core` 区块。适合放「用户名 / 核心偏好 / 长期目标」这类必须永远在线的事实。
>
> **recall explain（#4）**：召回结果里每条记忆都带 `source`（来源）和 `detail`（如 graph 的边类型），配合 `format=markdown` 能直观看到「这条记忆为什么被想起来」。

## 五、配置你的 CLI

### 5.1 生成 CLI 的接入配置

```bash
# 查看支持哪些 CLI
./brain gateway-config help

# 生成 OpenCode 的配置
./brain gateway-config opencode --token "你的 BRAIN_TOKEN"

# 生成 Codex 的配置
./brain gateway-config codex --token "你的 BRAIN_TOKEN"

# 生成 Aider 的配置
./brain gateway-config aider --token "你的 BRAIN_TOKEN"

# 生成 Claude Code 的配置
./brain gateway-config claude-code --token "你的 BRAIN_TOKEN"
```

### 5.2 贴进 CLI 的配置文件

**OpenCode** — 把输出贴进 `~/.opencode/config.json`(或项目目录下的 `opencode.json`):

```bash
./brain gateway-config opencode --token "你的 TOKEN" > opencode.json
```

**Codex** — 贴进环境变量或 `~/.codex/config.toml`。

**Aider** — export 那两行到 shell,或者写进 `.env`。

**Claude Code** — export `ANTHROPIC_BASE_URL` 和 `ANTHROPIC_API_KEY`,然后正常启动 claude。

## 六、切换 CLI

换 CLI 只需要重新生成一份配置——**记忆在脑的 Postgres 里,不受 CLI 影响**:

```bash
# 从 OpenCode 换到 Codex
./brain gateway-config codex --token "你的 TOKEN"
```

贴进 Codex 的配置 → 重启 Codex。记忆原封不动。

## 七、迁移已有数据(如果从 MyBrain V1 升级)

```bash
# 先预览有哪些数据
./brain migrate --dry-run

# 正式迁移
./brain migrate
```

迁移是幂等的(跑过的不重复),放心重跑。

## 八、停止

`Ctrl+C` 停止脑。数据库还在,重启即恢复。

---

有问题提 GitHub issue: https://github.com/damonlinyz/brain/issues
