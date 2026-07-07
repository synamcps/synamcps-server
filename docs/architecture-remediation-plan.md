# План работ по итогам архитектурного ревью

Дата ревью: 2026-07-06.
Основание: архитектурное ревью кодовой базы (main, коммит `d41338f`).

Этапы упорядочены по приоритету: каждый следующий имеет смысл начинать после завершения предыдущего, но задачи внутри этапа можно вести параллельно. У каждой задачи указаны затронутые файлы и критерий готовности (DoD).

---

## Этап 1 — Безопасность (немедленно)

### 1.1. Закрыть обход авторизации в MCP-прокси через fallback по slug
- **Проблема:** `findServerBySlug` (`internal/mcpproxy/manager.go:262`) при промахе по списку доступных серверов делает fallback в `store.GetServerBySlug`, а `findScope` возвращает zero value, который `allow()` трактует как «разрешено всё». Любой аутентифицированный пользователь может вызывать `tools/call`, `resources/read`, `prompts/get` чужого сервера.
- **Действия:**
  - Убрать fallback: если сервер не найден среди `servers`, возвращать `forbidden`.
  - Развести семантику «scope не найден» и «scope без ограничений»: `findScope` должен возвращать `(scope, ok bool)`.
- **Файлы:** `internal/mcpproxy/manager.go`.
- **DoD:** прямой вызов инструмента по имени `slug__tool` без ACL на сервер возвращает `forbidden`; тест на сценарий есть (см. 1.2).

### 1.2. Табличные тесты на логику авторизации
- **Проблема:** `evaluateStorageAccess` и `CanAccessMCPServer` — самая опасная логика проекта — не покрыты ни одним тестом.
- **Действия:** табличные тесты на `evaluateStorageAccess` (`internal/access/service.go`): роли, пересечение режимов токена, `AllowedPermissions`, platform_admin; на `CanAccessMCPServer` и `AvailableMCPServers` (`internal/mcpproxy/access.go`): с токеном/без, пустые скоупы, сервер вне ACL; регрессионный тест на баг 1.1.
- **Файлы:** новые `internal/access/service_test.go`, `internal/mcpproxy/access_test.go`, `internal/mcpproxy/manager_test.go`.
- **DoD:** негативные сценарии (нет ACL, отозванный/суженный токен) зафиксированы тестами; `go test ./...` зелёный.

### 1.3. Обезопасить режим `JWKSURL == "insecure"`
- **Проблема:** значение поля конфига отключает проверку подписи токена (`internal/auth/provider_router.go:65`); одна ошибка конфигурации в проде равна отключению аутентификации.
- **Действия:** завязать режим на явный флаг `devMode` (или переменную окружения), при активации писать громкий warning в лог при старте; запретить в сочетании с не-loopback listen-адресом либо задокументировать риск.
- **Файлы:** `internal/auth/provider_router.go`, `internal/config/config.go`, `configs/*`, `docs/auth-setup.md`.
- **DoD:** insecure-режим невозможно включить одним незаметным полем конфига; поведение описано в документации.

---

## Этап 2 — Единая система авторизации

### 2.1. Удалить легаси-путь `internal/policy` из сервисов
- **Проблема:** `knowledge.Service` ветвится `if s.access != nil { ... } else { policy... }` в `Save`, `IngestBinary`, `List`, `Delete`, `canReadDoc*`; поведение безопасности зависит от сборки в `main.go`, семантика двух путей уже разошлась (скоуп `knowledge.write.public` есть только в policy-ветке).
- **Действия:**
  - Сделать `access.Service` обязательной зависимостью `knowledge.Service` (в конструктор, см. 3.1).
  - Перенести недостающие правила из `policy.CanWrite`/`CanDelete` в access-модель (решить: нужны ли скоупы `knowledge.write.public` и т.п. — если да, встроить в `evaluateStorageAccess`; если нет, зафиксировать решение).
  - Удалить `internal/policy` либо оставить как внутреннюю деталь `access` (проверки visibility).
- **Файлы:** `internal/knowledge/service.go`, `internal/policy/access_filter.go`, `internal/access/service.go`, `cmd/server/main.go`, `tests/unit/policy_test.go`.
- **DoD:** в `internal/knowledge` нет ни одного `if s.access != nil`; правила публичной записи/удаления работают одинаково для всех путей и покрыты тестами.

### 2.2. Sentinel-ошибки вместо сравнения строк
- **Проблема:** `errors.New("forbidden")` / `"not found"` создаются в сервисах, а маппинг в статусы идёт сравнением `err.Error()` (`internal/mcp/server.go:461`, HTTP-хендлеры). JSON-RPC отдаёт всё как `-32000`.
- **Действия:**
  - Завести пакет доменных ошибок (например, `internal/domainerr`): `ErrForbidden`, `ErrNotFound`, `ErrRateLimited`, `ErrUnknownMethod`.
  - Заменить создание и проверку ошибок на sentinel + `errors.Is` по всем сервисам и хендлерам.
  - Единый маппер: доменная ошибка → HTTP-код и → JSON-RPC-код (включая `-32601` для unknown method).
- **Файлы:** `internal/knowledge/service.go`, `internal/access/*`, `internal/mcpproxy/*`, `internal/mcp/server.go`, `internal/httpapi/*`, `internal/transport/streamablehttp/handler.go`.
- **DoD:** ни одного сравнения текста ошибки в кодовой базе; forbidden стабильно даёт 403 / корректный JSON-RPC-код.

### 2.3. Явный `AccessContext` вместо контрабанды через context
- **Проблема:** `knowledge.Service` и `mcp.Server` достают токен и скоупы из `context` (`auth.AccessContextFromContext`, `internal/knowledge/service.go:401`), при этом `Principal` передаётся явно; доменный слой импортирует транспортный пакет `auth`.
- **Действия:** передавать `models.APIAccessContext` (или узкий срез из него) явным параметром в методы `knowledge.Service`; извлечение из context оставить только на границе HTTP.
- **Файлы:** `internal/knowledge/service.go`, `internal/mcp/server.go`, `internal/httpapi/*`.
- **DoD:** `internal/knowledge` не импортирует `internal/auth`; сигнатуры сервисов самодостаточны для тестов без сборки HTTP-контекста.

---

## Этап 3 — Сборка и инфраструктура

### 3.1. Конструкторы вместо двухфазной инициализации `Attach*`
- **Проблема:** `AttachAccess`, `AttachUsage`, `AttachProxy`, `AttachMCPStore`, `SetOpaqueTokenResolver` доставляют зависимости мутацией после конструктора; порядок вызовов в `main.go` — негласный контракт.
- **Действия:** обязательные зависимости — в параметры конструктора; действительно опциональные (proxy, usage) — через functional options или явный no-op интерфейс вместо nil-проверок в бизнес-логике.
- **Файлы:** `internal/knowledge/service.go`, `internal/mcp/server.go`, `internal/access/service.go`, `internal/auth/resource_server.go`, `cmd/server/main.go`.
- **DoD:** после конструктора объект полностью готов; nil-проверки опциональных зависимостей локализованы в no-op реализациях.

### 3.2. Один пул соединений Postgres
- **Проблема:** `access.NewStore`, `metapg.New`, `mcpproxy.NewStore`, `pgvector.New` создают четыре `pgxpool` на один DSN.
- **Действия:** создавать один `*pgxpool.Pool` в `main.go`, передавать его в конструкторы сторов; настроить размеры пула из конфига.
- **Файлы:** `cmd/server/main.go`, `internal/access/store.go`, `internal/storage/meta/postgres/store.go`, `internal/mcpproxy/store.go`, `internal/storage/vector/pgvector/store.go`, `internal/config/config.go`.
- **DoD:** к Postgres открыт один пул; graceful shutdown закрывает его (см. 3.4).

### 3.3. Версионированные миграции
- **Проблема:** каждый стор при старте выполняет свой `CREATE TABLE IF NOT EXISTS` / `ALTER TABLE`; нет версионирования, отката, порядок зависит от инициализации.
- **Действия:** выбрать инструмент (golang-migrate / goose / собственный embedded-каталог с таблицей версий); собрать текущие DDL из всех сторов в нулевую миграцию; выполнять миграции одним шагом при старте (или отдельной командой); убрать DDL из сторов.
- **Файлы:** новый каталог `migrations/`, `cmd/server/main.go`, `internal/access/store.go`, `internal/storage/meta/postgres/store.go`, `internal/mcpproxy/store.go`, `internal/storage/vector/pgvector/store.go`.
- **DoD:** схема воспроизводится с нуля из миграций; в сторах нет DDL; следующая правка схемы — отдельный версионированный файл.

### 3.4. Решение по in-memory-состоянию и graceful shutdown
- **Проблема:** `session.Store` дублирует данные в никогда не очищаемые map (утечка памяти, расхождение реплик); `mcpproxy.Manager.sessions` и фолбэки `usage.Service` — состояние на инстанс. Shutdown не закрывает пулы и не останавливает экспортер VictoriaMetrics.
- **Действия:**
  - Принять решение: либо один инстанс (зафиксировать в `docs/setup.md`), либо Redis обязателен — тогда убрать in-memory-фолбэки из `session.Store` и `usage.Service`.
  - Если фолбэки остаются — добавить эвикцию истёкших записей.
  - Прокинуть корневой context с отменой в экспортер; закрывать пул Postgres и Redis-клиент при shutdown.
- **Файлы:** `internal/session/redis_store.go`, `internal/usage/service.go`, `cmd/server/main.go`, `docs/setup.md`.
- **DoD:** зафиксирована модель деплоя; память не растёт от истёкших сессий; shutdown освобождает все ресурсы.

---

## Этап 4 — Масштабируемость и надёжность данных

### 4.1. Фильтрация доступных storage в SQL
- **Проблема:** `ReadableStorageIDs` / `AvailableStorages` грузят все storage и оценивают каждый в цикле — O(всех storage) на запрос.
- **Действия:** SQL-запрос с join по `storage_acl_bindings` и subject keys; `evaluateStorageAccess` оставить для расчёта effective-прав по уже отфильтрованному набору.
- **Файлы:** `internal/access/store.go`, `internal/access/service.go`.
- **DoD:** число строк, читаемых на запрос, пропорционально доступным storage, а не всем.

### 4.2. Убрать N+1 в MCP-access и в поиске
- **Проблема:** `AvailableMCPServers` вызывает `CanAccessMCPServer` в цикле — загрузка ACL на каждый сервер; `knowledge.Search` делает `catalog.Get` на каждый хит.
- **Действия:** в `AvailableMCPServers` загрузить ACL один раз и оценивать в памяти (по образцу `evaluateStorageAccess`); в `Search` — батч-загрузка документов по списку DocID (`catalog.GetMany`).
- **Файлы:** `internal/mcpproxy/access.go`, `internal/knowledge/service.go`, `internal/storage/meta/catalog.go`, `internal/storage/meta/postgres/store.go`.
- **DoD:** один ACL-запрос на вызов; один запрос в каталог на поисковый запрос.

### 4.3. Асинхронный ингест с консистентными статусами
- **Проблема:** пайплайн (`internal/knowledge/ingest/pipeline.go`) синхронно суммаризирует и эмбеддит в HTTP-запросе при `WriteTimeout: 20s`; вектора пишутся до записи документа в каталог — сбой оставляет сироты; статус `processing` в БД не существует.
- **Действия (поэтапно):**
  1. Писать документ в каталог со статусом `processing` **до** индексации; переводить в `ready`/`failed` по завершении — сироты становятся находимыми.
  2. Вынести суммаризацию/эмбеддинг в фоновый воркер (горутина + таблица заданий со статусами и retry); API отвечает сразу с `processing`.
  3. Согласовать порядок удаления (каталог → вектора либо пометка `deleting` + фоновая очистка).
- **Файлы:** `internal/knowledge/ingest/pipeline.go`, `internal/knowledge/service.go`, `internal/storage/meta/postgres/store.go`, миграция для таблицы заданий.
- **DoD:** сбой на любом шаге не оставляет невидимых сирот; долгий документ не убивает HTTP-запрос; статус в БД отражает реальность.

---

## Этап 5 — Протокол MCP и качество API

### 5.1. Довести custom MCP server/proxy до приемлемой типизации и Streamable HTTP
- **Проблема:** весь `internal/mcp` и `mcpproxy/client.go` — рукописный JSON-RPC на `map[string]any`: рекурсия `tools/call` → `HandleJSONRPC`, GET `/mcp` отдаёт один ping вместо SSE-стрима, `DELETE /mcp` — no-op, batch и notifications без `id` не по спеке.
- **Решение:** остаёмся на custom-реализации; переход на официальный `github.com/modelcontextprotocol/go-sdk` вынесен в `docs/backlog.md`.
- **Действия:** типизировать JSON-RPC request/response/error; убрать рекурсивный dispatch `tools/call`; реализовать batch и notifications без `id`; довести Streamable HTTP до долгоживущего SSE GET, replay по `Last-Event-ID` и удаления session по `DELETE /mcp`; типизировать proxy-клиент.
- **Файлы:** `internal/mcp/`, `internal/mcpproxy/client.go`, `internal/transport/streamablehttp/handler.go`, `internal/session/redis_store.go`, `docs/backlog.md`.
- **DoD:** batch/notifications обрабатываются по JSON-RPC-семантике; GET `/mcp` держит SSE stream, а не отдаёт один ping; DELETE очищает session; proxy-клиент не опирается на нетипизированный response envelope.

### 5.2. Структура зависимостей роутера и конфигов
- **Проблема:** `NewRouterWithAdmin` — 13 позиционных параметров (`internal/httpapi/router.go:18`); `ingest.Pipeline` и `auth.Gateway` принимают весь `config.Config`.
- **Действия:** заменить сигнатуру на `RouterDeps{}`; передавать компонентам узкие срезы конфига (`ChunkingConfig`, `S3Config`, ...).
- **Файлы:** `internal/httpapi/router.go`, `internal/knowledge/ingest/pipeline.go`, `internal/auth/*`, `cmd/server/main.go`.
- **DoD:** добавление зависимости роутера — новое поле структуры, а не правка всех вызовов; из сигнатур видно, что компонент реально использует.

### 5.3. Рудименты скаффолда и мелкий техдолг
- **Действия:**
  - Реальный score в результатах поиска вместо `Score: 1.0`; убрать заглушку `[]float32{0.1, 0.2}` в `embedQuery` (ошибка вместо фиктивного вектора).
  - `extractSnippet`: резать по рунам, не по байтам (кириллица).
  - `HandleInitialize`: пробрасывать request context вместо `context.Background()`, убрать жёсткий `id: "init"`.
  - Дедупликация: `asString` (3 пакета), `toolDescriptor` (`mcp` и `mcpproxy`), `hasScope`/`intersects`.
  - CORS: loopback-исключение в `main.go` — вынести в конфиг-флаг или задокументировать комментарием.
- **Файлы:** `internal/knowledge/service.go`, `internal/mcp/server.go`, `internal/mcpproxy/manager.go`, `cmd/server/main.go`.
- **DoD:** пункты закрыты или переведены в issues с обоснованием.

---

## Сводная таблица

| Этап | Задача | Приоритет | Оценка |
|------|--------|-----------|--------|
| 1.1 | Fallback-обход авторизации прокси | критический | 0.5 дня |
| 1.2 | Тесты на авторизацию | критический | 1–2 дня |
| 1.3 | Режим insecure JWKS | высокий | 0.5 дня |
| 2.1 | Единая авторизация, удаление policy-ветки | высокий | 2–3 дня |
| 2.2 | Sentinel-ошибки | высокий | 1–2 дня |
| 2.3 | Явный AccessContext | средний | 1 день |
| 3.1 | Конструкторы вместо Attach* | средний | 1–2 дня |
| 3.2 | Единый пул Postgres | средний | 0.5 дня |
| 3.3 | Версионированные миграции | высокий | 1–2 дня |
| 3.4 | In-memory-состояние и shutdown | высокий | 1–2 дня |
| 4.1 | Фильтрация storage в SQL | средний | 1 день |
| 4.2 | N+1 в MCP-access и поиске | средний | 1 день |
| 4.3 | Асинхронный ингест | средний | 3–5 дней |
| 5.1 | Спайк MCP Go SDK | средний | 1–2 дня (спайк) |
| 5.2 | RouterDeps и узкие конфиги | низкий | 1 день |
| 5.3 | Мелкий техдолг | низкий | 1–2 дня |

Примечание: оценки — грубые ориентиры для одного разработчика, без учёта ревью и выкладки. Этап 1 стоит выполнить до любых новых фич; этапы 2–3 снижают стоимость всех последующих изменений; этапы 4–5 можно приоритизировать по фактической нагрузке и планам из `docs/roadmap.md`.
