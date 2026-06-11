# Synamcps (SynaMCPs) — MCP + Knowledge Storage Gateway

`Synamcps` — это сервер, который предоставляет:

- **MCP-сервер** для LLM-клиентов (Cursor / Claude Desktop / Claude Code / и др.)
- **HTTP API** для сохранения/поиска/чтения knowledge-items
- **Web Admin UI** для управления пользователями/группами/хранилищами/tokens, просмотра статуса и базовой диагностики
- **Токенизированный доступ к storage** (token only *narrowing* прав), с ACL/RBAC, rate-limit и usage/metrics

Сервер поддерживает несколько способов аутентификации (OIDC/Keycloak/Google/Teleport Proxy JWT), а также **внутренний логин** для админки.

---

## Быстрый старт (Docker Compose)

Требования:

- Docker + Docker Compose

Запуск:

```bash
cp .env.example .env
make compose-up
```

Откройте:

- Web UI: `http://localhost:8080/login`
- MCP endpoint (streamable): `http://localhost:8080/mcp`
- HTTP API: `http://localhost:8080/api/*`

Остановка:

```bash
make compose-down
```

---

## Локальный запуск (без Docker)

Требования:

- Go **1.23+**
- Доступные Postgres, Redis, S3/MinIO (или используйте Docker Compose как инфраструктуру)

```bash
export CONFIG_PATH=configs/config.local.yaml
go run ./cmd/server
```

---

## Функциональность (в общих чертах)

### Storages, ACL и токены

- **Storage** — логическая сущность, к которой привязаны:
  - отдельные записи в metadata catalog (Postgres)
  - отдельные префиксы в S3 (`storage.S3Prefix`)
  - ограничения на поиск (векторный бэкенд — pgvector/qdrant)
- **ACL bindings** задают доступ пользователей/групп к storage (read/write/admin/owner).
- **Access tokens**:
  - принадлежат пользователю (owner)
  - **не расширяют**, а *сужают* права владельца: пересечение ACL пользователя и scopes токена
  - могут ограничивать: `storageIds`, `maxMode` (read/read_write), `toolAllowlist`, rate limits.
- **Видимость документов** (`personal`/`group`/`public`) проверяется поверх доступа к storage: доступа на чтение storage недостаточно — `personal`-документ виден только владельцу, `group`-документ — владельцу или участникам его групп.

### Knowledge items

- Item можно добавить как:
  - **Text** (через стандартный `POST /api/knowledge`)
  - **File** (upload → raw сохраняется в S3 → best-effort extraction → summary+embeddings → item в storage)
  - **Link** (download → raw в S3 → extraction → summary+embeddings → item в storage)
- **Link**-ingest принимает только `http`/`https` URL и отказывается ходить на внутренние адреса (loopback, link-local, включая cloud-metadata `169.254.169.254`, и приватные диапазоны) — защита от SSRF, работающая в том числе при редиректах.

### MCP

- MCP предоставляет **динамический tools/list** на основе bearer-токена (видны только разрешённые инструменты/хранилища).
- Поддерживается **Streamable HTTP** транспорт (`/mcp`) и опционально legacy SSE.
- Имена MCP tools используют `_` (underscore), чтобы избежать фильтрации/варнингов у некоторых клиентов.
- **MCP proxy**: upstream HTTP/SSE серверы в Admin UI (вкладка MCP Servers), discovery tools/resources/prompts, ACL и per-token scopes. Идентификаторы: `{slug}__{name}`, resources: `syna-mcp/{slug}/{uri}` (`slug` генерируется автоматически из имени сервера). Секреты шифруются в Postgres (`MCP_PROXY_SECRETS_KEY`).

### Usage / Rate limit / Metrics

- Rate limiting per token (минуты/часы/дни + burst), применяется **и** к MCP-вызовам, **и** к REST API (`429 Too Many Requests` при превышении).
- Размер тела запроса ограничен `limits.max_upload_bytes` (`413` при превышении).
- Usage события (и статус/ошибки) могут писаться в Redis TimeSeries (если включено).
- `/metrics` отдаёт Prometheus-формат метрик (значения лейблов экранируются, кардинальность серий ограничена).

### Web Admin UI

Встроенная админка (сервер рендерит HTML) позволяет:

- Users / Groups / Group members
- Storages + Storage details (ACL, keys/tokens, items list)
- Tokens + MCP Connect wizard + delete
- Add item (Text/File/Link)
- Search (по token / по storage)
- Status (Postgres/Redis/S3/LLMs + error counters)

Формы выбирают сущности из **выпадающих списков с именами** (storages/groups/users/tokens/MCP servers) с кнопками обновления — вместо ручного ввода ID. Slug больше не вводится руками: у storage он по умолчанию равен id, у MCP-сервера генерируется уникальным из имени.

---

## Конфигурация

Файл по умолчанию: `configs/config.example.yaml`  
Переопределение: `CONFIG_PATH=/path/to/config.yaml`

Ключевые секции:

- `web.default_admin`: логин/пароль админки (пароль — через env ref)
- `oauth.providers`: OIDC провайдеры (issuer/audience/jwks_url)
- `teleport`: Teleport Proxy JWT (issuer/audience)
- `redis`: сессии + usage/ts (если включено)
- `s3`: endpoint/bucket + порог больших документов
- `embedding`, `summarization`: LLMы (provider/model/api/api_key_env_ref)
- `vector_backend.active`: `pgvector` или `qdrant`
- `metadata_catalog.dsn`: Postgres DSN
- `api.allowed_origins`: строгий CORS allowlist
- `usage`: учёт и time series, retention, exporters

Пример `.env` для локальной разработки: `.env.example`.

---

## HTTP API

Аутентификация:

- **cookieAuth**: web UI сессия (`session_id` cookie)
- **bearerAuth**: `Authorization: Bearer <token>`

Ошибки (часто встречающиеся):

- `401` — нет токена/сессии
- `403` — forbidden (не хватает прав на storage/операцию)
- `404` — not found
- `413` — тело запроса превышает `limits.max_upload_bytes`
- `422` — invalid request
- `429` — превышен rate limit (лимиты per-token)

### Knowledge API

#### `GET /api/knowledge`

Список items с пагинацией и фильтрами.

Query params:

- `page` (int)
- `pageSize` (int)
- `storageId` (string) — ограничить выдачу только одним storage
- `source` (string) — exact match
- `sourceUrl` (string)
- `sourceUrlMode` (`exact` | `partial`) — partial учитывается только если `search.filters.source_url.allow_partial_match=true`

Response: `models.PaginatedKnowledgeList` (items + total + hasNext + page/pageSize).

#### `POST /api/knowledge`

Создаёт item из текста.

Body:

```json
{
  "storageId": "storage-id-optional",
  "title": "Runbook",
  "text": "Long knowledge text...",
  "mimeType": "text/plain",
  "visibility": "personal",
  "groupIds": [],
  "source": "api",
  "sourceUrl": "https://docs.example.com/runbook"
}
```

Примечания:

- если `storageId` пустой и включён access-service, сервер использует/создаёт personal storage пользователя
- `visibility` по умолчанию `personal`
- `groupIds` должен быть массивом (не `null`)

#### `GET /api/knowledge/{docId}`

Возвращает один документ.

#### `DELETE /api/knowledge/{docId}`

Удаляет документ (и связанные embeddings в vector store).

#### `POST /api/knowledge/search`

Поиск по embeddings.

Body:

```json
{
  "query": "kubernetes ingress timeout",
  "topK": 10,
  "filters": {
    "storageId": "storage-id-optional",
    "source": "api",
    "sourceUrl": "https://...",
    "sourceUrlMode": "exact"
  }
}
```

Response: массив search hits (включая snippet/title/source/sourceUrl).

### Ingest API (File/Link)

#### `POST /api/knowledge/ingest/file` (multipart)

Загружает файл как item:

- raw содержимое сохраняется в S3
- выполняется best-effort extraction
- pipeline делает summary + embeddings
- итог сохраняется как обычный knowledge item

Multipart fields:

- `storageId` (string, optional)
- `title` (string, optional)
- `visibility` (`personal`|`group`|`public`, optional)
- `source` (string, optional)
- `sourceUrl` (string, optional)
- `mimeType` (string, optional)
- `file` (required)

Пример:

```bash
curl -X POST http://localhost:8080/api/knowledge/ingest/file \
  -H "Authorization: Bearer $TOKEN" \
  -F "storageId=..." \
  -F "title=Spec" \
  -F "visibility=personal" \
  -F "file=@./spec.txt"
```

#### `POST /api/knowledge/ingest/link` (json)

Скачивает URL, сохраняет raw в S3, извлекает текст и сохраняет item.

Body:

```json
{
  "storageId": "storage-id-optional",
  "title": "Optional title",
  "url": "https://example.com/docs",
  "visibility": "personal",
  "source": "link"
}
```

---

## Admin API (`/api/admin/*`)

Все endpoints требуют аутентификацию (cookie или bearer), и многие требуют `platform_admin`.

### Users

- `GET /api/admin/me`
- `GET /api/admin/users` (platform_admin)
- `POST /api/admin/users` (platform_admin)
- `GET /api/admin/users/{id}` (admin или сам пользователь)
- `PATCH /api/admin/users/{id}` (admin или сам пользователь)
- `POST /api/admin/users/{id}/password` (admin или сам пользователь)
- `DELETE /api/admin/users/{id}` (platform_admin)

### Groups

- `GET /api/admin/groups` (platform_admin)
- `POST /api/admin/groups` (platform_admin)
- `DELETE /api/admin/groups/{id}` (platform_admin)
- `GET /api/admin/groups/{id}/members` (platform_admin)
- `PUT /api/admin/groups/{id}/members/{userId}` (platform_admin)
- `DELETE /api/admin/groups/{id}/members/{userId}` (platform_admin)

### Storages

- `GET /api/admin/storages` (доступные storages для текущего пользователя)
- `POST /api/admin/storages`
- `DELETE /api/admin/storages/{id}` (требует `storage.delete`: owner/admin storage или platform_admin)
- `GET /api/admin/storages/{id}` (storage details: storage + acl + tokens; требует доступ на чтение)
- `GET /api/admin/storages/{id}/acl` (требует `acl.manage`)
- `PUT /api/admin/storages/{id}/acl` (требует `acl.manage`)

### Tokens

Изменяющие endpoints требуют, чтобы вызывающий был **владельцем токена или platform_admin**; `GET /api/admin/tokens` отдаёт только собственные токены (platform_admin видит все).

- `GET /api/admin/tokens`
- `POST /api/admin/tokens`
- `DELETE /api/admin/tokens/{id}` (owner/platform_admin)
- `PATCH /api/admin/tokens/{id}/rate-limit` (owner/platform_admin)
- `POST /api/admin/tokens/{id}/revoke` (owner/platform_admin)
- `POST /api/admin/tokens/{id}/rotate` (owner/platform_admin)
- `PATCH /api/admin/tokens/{id}/mcp-scopes` (owner/platform_admin)
- `GET/POST /api/admin/tokens/{id}/connect-options` (wizard для MCP клиентов)

### Usage / Status

- `GET /api/admin/usage/series`
- `GET /api/admin/usage/summary`
- `GET /api/admin/status` — статус компонентов + error counters (Redis TimeSeries)

---

## MCP

### Transport

- Streamable HTTP: `POST /mcp` (JSON-RPC) + `GET /mcp` (SSE stream по `Mcp-Session-Id`)
- Legacy SSE (если включено): `/sse` + `/messages`

### Минимальный flow (streamable)

1) Получить bearer token (OIDC/Teleport/или внутренний)  
2) `initialize`:

```bash
curl -X POST http://localhost:8080/mcp \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":"1","method":"initialize","params":{}}'
```

3) Сохранить `Mcp-Session-Id` из заголовка/ответа (клиент делает это автоматически)  
4) Открыть stream (bearer-токен обязателен и должен совпадать с владельцем сессии):

```bash
curl -N http://localhost:8080/mcp \
  -H "Authorization: Bearer $TOKEN" \
  -H "Mcp-Session-Id: <session_id>"
```

`GET /mcp` и `DELETE /mcp` аутентифицируются; сессию может читать/закрывать только создавший её principal.

### Dynamic tools/list

`tools/list` возвращает только те инструменты, которые разрешены текущим bearer-токеном и его storage scopes.

### tools/call

`tools/call` маршрутизирует вызовы на соответствующие внутренние методы (knowledge_* и т.д.).

### MCP Connect (Web UI)

В админке есть страница **MCP Connect**, которая генерирует:

- имя конфигурационного файла
- `configBody` (JSON)
- пошаговые инструкции

`configBody` можно копировать кликом.

---

## Установка и эксплуатация

### Docker Compose

```bash
cp .env.example .env
make compose-up
```

Полезно:

- `make compose-down`
- `make seed-dev` (если скрипт используется в вашей среде)

### Конфиг и секреты

- `configs/config.example.yaml` — пример конфига
- `configs/config.local.yaml` — локальный конфиг для compose (используется в `docker-compose.yml`)
- `.env` — секреты (пароли/ключи), пример в `.env.example`

### CORS

`api.allowed_origins` — строгий allowlist. Если origin неизвестен — запрос отклоняется.
Web UI роуты (`/`, `/login`, `/logout`, `/app*`) обходят origin-check.

---

## Troubleshooting

- **Origin not allowed**:
  - добавьте origin в `api.allowed_origins`
  - убедитесь, что вы открываете Web UI через `/login` (web routes bypass origin-check)
- **Invalid credentials**:
  - проверьте, что `.env` подключён (в compose это делается через `env_file: .env`)
  - проверьте `web.default_admin.password_env_ref`
- **Не создаются time series**:
  - нужен Redis с RedisTimeSeries модулем (команда `TS.ADD` должна поддерживаться)
  - либо отключите `usage.redis_timeseries`
- **MCP tools “filtered out” warning**:
  - инструменты уже используют `_` вместо `.`

---

## Документация в репозитории

- `docs/setup.md` — установка и запуск
- `docs/api.md` — базовые knowledge endpoints
- `docs/mcp-connection.md` — подключение MCP
- `docs/auth-setup.md` — auth провайдеры
- `docs/openapi.yaml` — базовая OpenAPI заготовка

