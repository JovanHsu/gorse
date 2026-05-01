# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Gorse is an AI-powered, open-source recommender system written in Go. It automatically trains models from imported items, users, and interaction data to generate personalized recommendations. Supports multi-source recommendations (latest, user-to-user, item-to-item, collaborative filtering), multimodal embeddings, and LLM-based recommenders.

## Build Commands

```bash
# All-in-one node (playground mode for quick start)
go run cmd/gorse-in-one/main.go --playground

# All-in-one with config
go run cmd/gorse-in-one/main.go --config config/config.toml

# Distributed nodes
go run cmd/gorse-master/main.go --config config/config.toml
go run cmd/gorse-server/main.go
go run cmd/gorse-worker/main.go

# Admin CLI tool
go run cmd/goat/main.go

# Benchmark tool
go run cmd/gorse-bench/main.go

# Docker
docker compose up -d   # master + worker + server + MySQL
docker buildx bake      # build all Docker images
docker run -p 8088:8088 zhenghaoz/gorse-in-one --playground  # all-in-one playground

# Linting (config: .golangci.yml)
golangci-lint run ./... --timeout 20m
```

## Test Commands

```bash
# Start test databases (required for integration tests)
cd storage && docker compose up -d

# Run all tests
go test -v ./...

# Run a single test
go test -v ./storage/cache/... -run TestRedis

# With coverage
go test -v ./... -coverprofile=coverage.txt -covermode=atomic -coverpkg=./...

# Race detection
go test -race ./...

# Skip database-specific tests (no Docker DB running)
go test -v ./... -skip "TestPostgres|TestMySQL|TestMongo|TestRedis|TestClickHouse|TestMilvus|TestQdrant|TestWeaviate"
```

Test databases use environment variables (or defaults):
- `MYSQL_URI=mysql://root:password@tcp(127.0.0.1:3306)/`
- `POSTGRES_URI=postgres://gorse:gorse_pass@127.0.0.1/`
- `REDIS_URI=redis://127.0.0.1:6379/`
- `MONGO_URI=mongodb://root:password@127.0.0.1:27017/`
- `CLICKHOUSE_URI=clickhouse://127.0.0.1:8123/`

## Docker Setup

```bash
# All-in-one (playground mode)
docker run -p 8088:8088 zhenghaoz/gorse-in-one --playground

# Cluster via Docker Compose
mkdir github && cd github
wget https://raw.githubusercontent.com/gorse-io/gorse/master/docker-compose.yml
wget https://raw.githubusercontent.com/gorse-io/gorse/master/config/config.toml -P config
docker compose up -d

# Import sample data (GitHub dataset with users, repos, interactions)
wget https://cdn.gorse.io/example/github.bin.gz
gzip -d github.bin.gz
curl -X POST --data-binary @github.bin http://localhost:8088/api/restore
docker compose restart master
```

**REST API ports**: gorse-in-one uses port `8088`; cluster server node uses port `8087`.

## API Quick Reference

```bash
# Insert feedback (POST = accumulate value, PUT = overwrite)
curl -X POST http://localhost:8088/api/feedback \
   -H 'Content-Type: application/json' \
   -d '[{"FeedbackType":"star","UserId":"bob","ItemId":"ollama:ollama","Value":1,"Timestamp":"2022-02-24"}]'

# Get recommendations
curl "http://localhost:8088/api/recommend/bob?n=10"
curl "http://localhost:8088/api/recommend/bob?write-back-type=read&write-back-delay=10m&n=10"

# Auto-read: write-back-type=read marks items as read; write-back-delay=Nm defers by N minutes
```

## Data Model

- **User**: `{UserId, Labels, Comment}` — `UserId` must not contain `/`
- **Item**: `{ItemId, IsHidden, Categories, Timestamp, Labels, Comment}` — `ItemId` must not contain `/`; `IsHidden=true` removes from recommendations immediately
- **Feedback**: `{FeedbackType, UserId, ItemId, Value, Timestamp}` — unique by (UserId, ItemId, FeedbackType)
  - **Positive**: user actions you want to encourage (e.g., `star`, `like`, `read>=3`)
  - **Read**: items the user has seen
  - **Negative**: explicit dislikes (highest priority, permanently blocks item for that user)

Key config options (`config.toml`):
```toml
[recommend.data_source]
positive_feedback_types = ["star","like","read>=3"]
read_feedback_types = ["read"]
negative_feedback_types = ["dislike"]
positive_feedback_ttl = 0    # days, 0 = never expire
item_ttl = 0                # days, 0 = never expire

[recommend]
cache_size = 100
cache_expire = "72h"
```

## Recommendation Pipeline (Retrieve → Rank)

```
[Users, Items, Feedback]
        ↓
  Retrieval Layer (multiple recommenders)
    - latest           : newest items
    - non-personalized : rule-based (e.g., most starred)
    - user-to-user     : "people like you liked this"
    - item-to-item     : "because you liked X, try Y"
    - collaborative   : matrix factorization on user-item matrix
    - external         : third-party API
        ↓
  Ranking Layer
    - Ranker (FM factorisation machine or LLM reranker)
    - Replacement (re-insert read items after delay)
    - Fallback (new users get latest/popular items)
        ↓
  Server Node: filter read items, return final list
```

Master node responsibilities: load dataset, compute user/item neighbors, train CF model, train CTR (FM) model.
Worker node responsibilities: run retrieve + rank pipeline, write offline recommendations to cache.
Server node responsibilities: serve REST API, filter read items, apply fallback.

## Architecture

### Cluster Model
Single-node training / distributed prediction:
- **Master node** (`master/`): Model training, non-personalized recommendations, config/membership, REST API + Dashboard (gRPC:8086, HTTP:8088)
- **Server node** (`server/`): REST API and online real-time recommendations (port 8087)
- **Worker node** (`worker/`): Offline batch recommendations per user (port 8089)
- **All-in-one** (`cmd/gorse-in-one/`): Combines all three in one process

### Key Packages

| Path | Purpose |
|---|---|
| `cmd/gorse-{master,server,worker,in-one,goat,gorse-bench}/` | Entry points (Cobra CLI) |
| `master/` | Master node: REST API, task scheduler, metrics |
| `server/` | Server node: REST API for recommendations |
| `worker/` | Worker node: offline recommendation pipeline |
| `storage/` | DB abstraction: `data/`, `meta/`, `cache/`, `vectors/`, `blob/` |
| `model/` | ML models: `cf/` (collaborative filtering), `ctr/` (click-through rate) |
| `logics/` | Recommendation strategies: `cf.go`, `user_to_user.go`, `item_to_item.go`, `non_personalized.go`, `external.go`, `chat.go` |
| `common/` | Utilities: `ann/` (approximate nearest neighbors), `blas/`, `nn/` (neural networks), `reranker/`, `parallel/`, `monitor/` |
| `config/` | Configuration loading (Viper) |
| `protocol/` | gRPC protocol definitions (`.proto`) |
| `client/` | Go client library |
| `dataset/` | Dataset loading and processing |

### Storage Backends
- **Meta/Cache**: MySQL, PostgreSQL, MongoDB, ClickHouse, Redis, SQLite
- **Vectors**: Qdrant, Weaviate, Milvus
- **Blob**: S3, GCS, Azure Blob, Local filesystem

### Clock Synchronization
Gorse supports future-timestamped feedback and depends on synchronized clocks across nodes. Set `clock_error` in config to the maximum expected clock drift between hosts.

### Tech Stack
- Go 1.26, gRPC + Protocol Buffers, GORM, Zap logging, Cobra CLI, Viper config
- Prometheus client, OpenTelemetry tracing
- Gomlx for neural network execution, C-bata/goptuna for hyperparameter tuning
