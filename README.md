# 教室排课助手

教室排课助手是一个纯后端 RESTful API 服务，为学校和培训机构提供课程表编排、教室资源管理和冲突检测能力。

## 项目主要功能

- **基础数据管理**：教室、教师、班级、课程、时间段的完整 CRUD API。
- **智能排课算法**：根据学期周数、每周天数、每天节数和课程周课时要求生成课表，避开教师/班级/教室时间冲突，优先满足连排需求。
- **待发布草稿**：智能排课、换课、移动均先写入待发布草稿，不会直接改动正式课表，避免值班老师误触影响全校安排。
- **草稿发布与冲突拦截**：发布前校验草稿，存在冲突时返回 409 与冲突清单，正式课表保持不变；发布成功后草稿才成为正式安排。
- **课表版本历史**：每次发布会把旧正式课表归档为历史版本，形成完整版本记录；查询与导出默认读取最新正式版本，也可按版本回看历史。
- **冲突检测与报告**：检测教师时间冲突、班级时间冲突、教室时间冲突、教室容量冲突和教师偏好冲突，并给出解决建议。
- **课表查询与导出**：按班级、教师、教室查询课表，支持 JSON / CSV 导出，支持按周次查看，支持按历史版本查询/导出。
- **调课与手动调整**：支持交换两节课、移动单节课到空闲时段（先落入草稿），自动重新检测冲突并记录调课历史。
- **统计与利用率分析**：教室利用率、教师工作量、课程分布热力图数据。

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
| POST | `/api/v1/schedules/generate` | 智能排课（结果写入待发布草稿） |
| GET | `/api/v1/schedules` | 课表查询（默认最新正式版本，支持 `week` / `version_id`） |
| GET | `/api/v1/schedules/conflicts` | 正式课表冲突检测 |
| GET | `/api/v1/schedules/draft` | 查看待发布草稿（支持 `week` 按周筛选） |
| POST | `/api/v1/schedules/publish` | 发布草稿（请求体可传 `weeks` 按周发布；冲突时返回 409 与冲突清单） |
| GET | `/api/v1/schedules/versions` | 正式课表版本历史 |
| GET | `/api/v1/schedules/versions/:id` | 版本详情 |
| GET | `/api/v1/schedules/versions/:id/entries` | 版本课表明细（支持 `week`） |
| POST | `/api/v1/schedules/swap` | 交换两节课（先写入草稿） |
| POST | `/api/v1/schedules/move` | 移动单节课（先写入草稿） |
| GET | `/api/v1/schedules/adjustments` | 调课历史 |
| GET | `/api/v1/schedules/export` | 课表导出（JSON/CSV，默认最新正式版本，支持 `version_id`） |
| GET | `/api/v1/statistics/classrooms` | 教室利用率 |
| GET | `/api/v1/statistics/teachers` | 教师工作量 |
| GET | `/api/v1/statistics/density` | 课程分布热力图 |

### 草稿与发布流程

为避免误触"生成排课"直接覆盖全校正式课表，排课变更采用草稿 + 显式发布两段式：

1. `POST /api/v1/schedules/generate` 生成课表、`POST /api/v1/schedules/swap` 换课、`POST /api/v1/schedules/move` 移动，都只改待发布草稿，正式课表保持原样可查。
2. `GET /api/v1/schedules/draft?week=1` 查看草稿（含按周筛选与当前冲突列表）。
3. `POST /api/v1/schedules/publish` 发布草稿：请求体 `{"weeks":[1,2],"note":"第1-2周调整"}`，不传 `weeks` 表示发布草稿全部周次。草稿存在冲突时返回 HTTP 409，`data.conflicts` 为冲突清单，正式课表不变。
4. 发布成功后草稿成为正式安排，旧正式安排归档到版本历史（`GET /api/v1/schedules/versions`，明细 `/api/v1/schedules/versions/:id/entries`）。
5. `GET /api/v1/schedules` 与 `/api/v1/schedules/export` 默认读最新正式版本；传 `version_id` 可查询/导出历史版本。

> 首次换课/移动时若尚无草稿，系统会自动以当前正式课表初始化草稿，再在草稿上应用调整。

统一响应格式：

```json
{"code": 0, "message": "ok", "data": {}}
```

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
