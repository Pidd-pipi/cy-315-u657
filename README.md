# 教室排课助手

教室排课助手是一个纯后端 RESTful API 服务，为学校和培训机构提供课程表编排、教室资源管理和冲突检测能力。

## 项目主要功能

- **基础数据管理**：教室、教师、班级、课程、时间段的完整 CRUD API。
- **智能排课算法**：根据学期周数、每周天数、每天节数和课程周课时要求生成课表，避开教师/班级/教室时间冲突，优先满足连排需求。
- **草稿与发布工作流**：生成、换课、移动只写入待发布草稿，不影响正式课表；发布时整体校验冲突，有冲突则拒绝并返回冲突清单，成功后草稿成为新的正式版本，旧正式安排保留在版本历史中。查询与导出默认读取每周最新正式版本，也可按 `version_id` 查看历史版本。
- **冲突检测与报告**：检测教师时间冲突、班级时间冲突、教室时间冲突、教室容量冲突和教师偏好冲突，并给出解决建议。存在草稿时检测草稿，否则检测正式课表。
- **课表查询与导出**：按班级、教师、教室查询课表，支持 JSON / CSV 导出，支持按周次查看。
- **调课与手动调整**：支持交换两节课、移动单节课到空闲时段（均作用于草稿），自动重新检测冲突并记录调课历史。
- **统计与利用率分析**：教室利用率、教师工作量、课程分布热力图数据（基于最新正式版本）。

## API 文档

- Swagger UI：`/docs`
- OpenAPI JSON：`/swagger/doc.json`

## 快速启动

### Docker Compose（推荐）

```bash
docker compose --env-file .env up -d --build --wait
curl http://127.0.0.1:19515/healthz
```

停止并清理：

```bash
docker compose --env-file .env down -v --remove-orphans
```

### 本地运行

```bash
cd backend
go mod tidy
go run ./cmd/server
```

默认监听 `8080` 端口，SQLite 数据文件位于 `./data/gbschedule.db`。

## 主要 API 端点

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/healthz` / `/health` | 健康检查 |
| GET | `/docs` | Swagger UI |
| GET/POST | `/api/v1/classrooms` | 教室列表 / 新建教室 |
| GET/PUT/DELETE | `/api/v1/classrooms/:id` | 教室详情 / 更新 / 删除 |
| GET/POST | `/api/v1/teachers` | 教师列表 / 新建教师 |
| GET/PUT/DELETE | `/api/v1/teachers/:id` | 教师详情 / 更新 / 删除 |
| GET/POST | `/api/v1/classes` | 班级列表 / 新建班级 |
| GET/PUT/DELETE | `/api/v1/classes/:id` | 班级详情 / 更新 / 删除 |
| GET/POST | `/api/v1/courses` | 课程列表 / 新建课程 |
| GET/PUT/DELETE | `/api/v1/courses/:id` | 课程详情 / 更新 / 删除 |
| GET/POST | `/api/v1/time-slots` | 时间段列表 / 新建时间段 |
| GET/PUT/DELETE | `/api/v1/time-slots/:id` | 时间段详情 / 更新 / 删除 |
| POST | `/api/v1/schedules/generate` | 智能排课（写入待发布草稿） |
| GET | `/api/v1/schedules` | 正式课表查询（默认最新版本，支持 `week`/`version_id` 等筛选） |
| GET | `/api/v1/schedules/draft` | 查看待发布草稿（支持 `week` 筛选） |
| POST | `/api/v1/schedules/publish` | 发布草稿为新正式版本（支持 `week` 或 `weeks` 筛选，冲突返回 409 清单） |
| GET | `/api/v1/schedules/versions` | 正式版本历史 |
| GET | `/api/v1/schedules/conflicts` | 冲突检测（有草稿检草稿，否则检正式；支持 `week`） |
| POST | `/api/v1/schedules/swap` | 交换两节课（作用于草稿） |
| POST | `/api/v1/schedules/move` | 移动单节课（作用于草稿） |
| GET | `/api/v1/schedules/adjustments` | 调课历史（含发布记录） |
| GET | `/api/v1/schedules/export` | 正式课表导出（JSON/CSV，默认最新版本） |
| GET | `/api/v1/statistics/classrooms` | 教室利用率 |
| GET | `/api/v1/statistics/teachers` | 教师工作量 |
| GET | `/api/v1/statistics/density` | 课程分布热力图 |

统一响应格式：

```json
{"code": 0, "message": "ok", "data": {}}
```

## 草稿与发布工作流

为避免值班老师误触"生成排课"直接覆盖全校正式课表，所有写操作都先进入**待发布草稿**，正式课表只能通过发布变更：

1. `POST /api/v1/schedules/generate` 生成课表 → 整体替换草稿，正式课表不变。
2. `POST /api/v1/schedules/swap`、`POST /api/v1/schedules/move` 调整草稿。尚无草稿时，先从事务内复制当前最新正式课表作为草稿，再按传入的正式条目定位其草稿副本；传入不存在的 ID 会整体回滚，不会留下草稿。
3. `GET /api/v1/schedules/draft?week=N` 随时核对草稿。
4. `POST /api/v1/schedules/publish`（或 `?week=N` / `{"weeks":[1,2]}`）发布：
   - 草稿存在冲突时返回 `409`，`data.conflicts` 为冲突清单，草稿保留、正式课表不动；
   - 无草稿可发时返回 `404`；
   - 成功后草稿条目挂到一个不可变的新版本上；旧版本条目原样保留。
5. `GET /api/v1/schedules`、`/export`、统计接口默认读取**每周最新正式版本**；加 `version_id` 可读任意历史版本；`GET /api/v1/schedules/versions` 列出版本历史。

升级前已存在的课表数据会在服务启动时自动登记为 v1 正式版本，无需人工干预。

## 技术栈

| 层级 | 技术 |
| --- | --- |
| 语言 | Go 1.22 |
| Web 框架 | Gin |
| ORM | GORM |
| 数据库 | SQLite（github.com/glebarez/sqlite） |
| 参数校验 | go-playground/validator/v10 |
| 日志 | log/slog |
| API 文档 | swaggo/swag + swaggo/gin-swagger |

## 项目目录结构

```text
.
├── backend/
│   ├── Dockerfile
│   ├── go.mod
│   ├── go.sum
│   ├── cmd/server/main.go
│   ├── docs/
│   └── internal/
│       ├── config/
│       ├── constants/
│       ├── dto/
│       ├── handler/
│       ├── middleware/
│       ├── model/
│       ├── repository/
│       ├── router/
│       └── service/
├── api/
├── deploy/
├── migrations/
├── docker-compose.yml
├── .env
├── .env.example
└── README.md
```

## 本地开发命令

```bash
cd backend
go mod tidy
go run ./cmd/server
```

## Docker 部署说明

- 后端服务内部端口固定为 `8080`。
- 宿主端口由 `.env` 中的 `BACKEND_PORT` 控制，默认 `19515`。
- SQLite 数据通过命名卷 `gbschedule_data` 持久化到 `/app/data`。
- 镜像使用 Go 多阶段构建，运行在 `alpine:3.20`。

## License

MIT
