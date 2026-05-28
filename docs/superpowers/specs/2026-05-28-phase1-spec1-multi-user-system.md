# Spec: Multi-user System (Phase 1 — Spec 1/2)

**Date:** 2026-05-28
**Phase:** 1 — Identity Foundation
**Scope:** Users table, session linking, migration từ single-admin, user management API, auth settings API.
**Out of scope:** RBAC permission enforcement (Spec 2), SSO (Phase 4), SCIM (Phase 5).

---

## Context

Bifrost hiện có một admin duy nhất: `admin_username` / `admin_password` lưu trong bảng `governance_config` (key-value). `admin_password` **đã là bcrypt hash** (code tại `lib/config.go` chỉ hash nếu `!isBcryptHash()`). Sessions không liên kết với user nào. Spec này thay thế mô hình đó bằng một users table thực sự, đồng thời giữ nguyên trải nghiệm của admin hiện tại — không ai bị đăng xuất sau upgrade.

---

## Data Model

### Bảng mới: `users`

```
id             VARCHAR(255)  PRIMARY KEY              -- uuid
email          VARCHAR(255)  NOT NULL  UNIQUE INDEX   -- login identity, lowercase
name           VARCHAR(255)  NOT NULL
role           VARCHAR(50)   NOT NULL  DEFAULT 'viewer' -- 'admin'|'operator'|'viewer'
password_hash  TEXT          NULL                     -- nil = chỗ trống cho SSO (Phase 4)
is_active      BOOLEAN       NOT NULL  DEFAULT true
last_login_at  TIMESTAMP     NULL
created_at     TIMESTAMP     NOT NULL  INDEX
updated_at     TIMESTAMP     NOT NULL
```

`password_hash` nullable ngay từ đầu để schema không cần thay đổi khi SSO được thêm vào Phase 4. `role` lưu dạng string nhất quán với các bảng khác trong codebase.

### Thay đổi bảng `sessions`

Thêm một cột:

```
user_id  VARCHAR(255)  nullable  -- Go type: *string, không có DB-level FK hay not null tag
```

Logically required nhưng **không enforce bằng DB constraint** — cột nullable để tương thích với SQLite và Nhánh B (auth tắt). Postgres enforce NOT NULL ở Nhánh A sau khi backfill. Application enforce khi tạo session mới: `user_id` phải luôn được set (nếu null → 401, xem session flow bên dưới).

---

## Migration

### DB Migration (một migration, dialect-aware)

Migration phải rẽ nhánh theo `tx.Dialector.Name()` theo đúng pattern hiện tại. `user_id` **không dùng raw FK `REFERENCES`** — convention của codebase là cột varchar thường + GORM association tag, không phải DB-level FK constraint (xem `TableTeam.CustomerID`, `TableVirtualKey.TeamID`). Cách này cũng tránh conflict với giới hạn SQLite.

**Nhánh A — có admin credentials** (`governance_config` có `admin_username` VÀ `admin_password`):

```
1. CREATE TABLE users

2. INSERT INTO users:
       id            = uuid mới  (ghi nhớ là admin_uuid)
       email         = "admin@localhost"
       name          = governance_config["admin_username"]
       role          = "admin"
       password_hash = governance_config["admin_password"]  ← copy thẳng, KHÔNG hash lại
       is_active     = true

3. ADD COLUMN sessions.user_id VARCHAR(255)   ← nullable, không REFERENCES
   UPDATE sessions SET user_id = '<admin_uuid>'  ← backfill tất cả sessions cũ

4. Enforce NOT NULL (dialect-aware):
       Postgres: ALTER TABLE sessions ALTER COLUMN user_id SET NOT NULL
       SQLite:   bỏ qua — SQLite không hỗ trợ ALTER COLUMN; application enforces
```

**Nhánh B — không có admin credentials** (auth tắt hoặc chưa cấu hình):

```
1. CREATE TABLE users  (để rỗng)

2. ADD COLUMN sessions.user_id VARCHAR(255) nullable
   (không cần UPDATE vì sessions luôn rỗng khi auth tắt — không có login)
```

Hệ thống khởi động bình thường.

**Bootstrap admin đầu tiên ở Nhánh B:** Khi auth tắt (`is_auth_enabled = false`), `AuthMiddleware` set `IsLocalAdmin = true` và bypass mọi permission check (behavior hiện tại tại `middlewares.go:856-864`). Người vận hành bootstrap theo thứ tự: trong khi auth còn tắt → gọi `POST /api/users` để tạo admin đầu tiên → rồi enable auth.

**Lưu ý:** Các annotation "Role: admin" trong phần API của spec này là *documented intent* — enforcement thực tế được implement ở Spec 2 (RBAC). Ở Phase 1, role được lưu vào DB nhưng chưa được check ở middleware.

**Tại sao copy password_hash mà không hash lại:**
`admin_password` trong `governance_config` đã là bcrypt hash (`lib/config.go` chỉ hash nếu `!isBcryptHash()`). Hash lần hai tạo `bcrypt(bcrypt(pw))` — login kế tiếp sẽ fail.

`admin_username` / `admin_password` trong `governance_config` **không bị xoá** sau migration — login handler ngừng đọc chúng nhưng chúng vẫn tồn tại để an toàn nếu cần rollback.

**Lưu ý về admin@localhost:**
Email `admin@localhost` là placeholder. Admin có thể cập nhật email thật qua `PUT /api/users/:id` sau khi login. Release notes cần thông báo rõ email này.

---

## Session Behavior

### Authenticated request flow

Mỗi request authenticated thực hiện theo thứ tự:

```
1. Đọc token từ cookie "token" hoặc header "Authorization: Bearer <token>"
2. Tra sessions table → lấy session
3. Nếu session.user_id = null hoặc không tìm thấy session → 401  (edge case: session cũ/lạ)
4. Load user từ users table WHERE id = session.user_id
5. Nếu user không tồn tại hoặc is_active = false → 401 Unauthorized
6. Gắn user vào request context để handler và middleware dùng
```

Role không được cache trong session — luôn load fresh từ DB mỗi request. Thêm một DB query per request so với hiện tại (chấp nhận được với use case admin dashboard). Đảm bảo thay đổi role có hiệu lực ngay lập tức.

**Refactor `validateSession`:** Hàm hiện tại (`handlers/middlewares.go:644`) trả về `bool`. Cần refactor để trả về `(*tables.SessionsTable, *tables.TableUser, error)` và sửa tất cả call-site (WS ticket, `?token=`, cookie, Bearer — khoảng 4 nhánh trong `AuthMiddleware`).

### Session invalidation rules

| Sự kiện | Hành động |
|---------|-----------|
| User deactivate (`is_active = false`) | Không xoá session — request kế tiếp tự `401` |
| Role thay đổi | Không xoá session — role mới có hiệu lực ngay request kế tiếp |
| Admin reset password của user khác | Xoá **toàn bộ** sessions của user đó |
| User tự đổi password | Xoá tất cả sessions **khác**, giữ session hiện tại |
| Logout | Xoá session hiện tại |

### Session expiry

Lưu trong `governance_config`:

```
Key:     "session_expiry_hours"
Value:   "720"  (default = 30 ngày, backward compatible)
Min:     1 giờ
Max:     8760 giờ (1 năm)
```

Áp dụng cho **sessions mới**. Sessions đang tồn tại không bị ảnh hưởng khi thay đổi cấu hình. Chỉ `admin` được thay đổi.

---

## Cluster Sync

**Không cần thêm gì.** Cluster Bifrost dùng shared Postgres — users table đã consistent trên tất cả nodes. Vì session handler load user fresh từ DB mỗi request (không in-memory cache), thay đổi role hay deactivate trên Node A có hiệu lực ngay trên Node B ở request kế tiếp mà không cần event.

`session_expiry_hours` đã được cover bởi `UpdateAuthConfig` → `scheduleEvent("auth_config", ...)` (đã có trong `PublishingConfigStore`). Các node reload `AuthConfig` khi nhận event này.

---

## API

### User Management

| Method | Route | Ai được gọi | Mô tả |
|--------|-------|-------------|-------|
| `GET` | `/api/users` | admin | Danh sách tất cả users |
| `POST` | `/api/users` | admin | Tạo user mới |
| `GET` | `/api/users/me` | mọi role | Thông tin user đang đăng nhập |
| `GET` | `/api/users/:id` | admin hoặc chính user đó | Chi tiết một user |
| `PUT` | `/api/users/:id` | admin hoặc chính user đó | Cập nhật name, email |
| `PUT` | `/api/users/:id/role` | admin (không được đổi role của chính mình) | Đổi role |
| `PUT` | `/api/users/:id/password` | admin hoặc chính user đó | Đổi password |
| `PUT` | `/api/users/:id/active` | admin (không được deactivate chính mình) | Activate / deactivate |

**Không có `DELETE /api/users/:id`** — users chỉ được deactivate để giữ audit history.

---

#### `POST /api/users` — tạo user

Request:
```json
{ "email": "alice@acme.com", "name": "Alice", "role": "operator", "password": "..." }
```
Rules: email unique, password ≥ 8 ký tự, role phải là `admin`/`operator`/`viewer`. Password hash dùng `encrypt.Hash()` (bcrypt DefaultCost = 10, nhất quán với codebase).

---

#### `PUT /api/users/:id/password` — hai mode tuỳ người gọi

| Người gọi | Request body | Hành động |
|-----------|-------------|-----------|
| Admin reset password user khác | `{ "new_password": "..." }` | Hash bằng `encrypt.Hash()`, xoá **toàn bộ** sessions của user đó |
| User tự đổi password | `{ "current_password": "...", "new_password": "..." }` | Verify current bằng `encrypt.CompareHash()`, xoá tất cả sessions **khác** |

---

#### `PUT /api/users/:id/active`

Request: `{ "active": false }` hoặc `{ "active": true }`

Rules:
- Không được deactivate chính mình
- Không được deactivate admin cuối cùng (xem định nghĩa ở Business Rules)

---

### Auth Settings

#### `GET /api/auth/settings`
- **Role:** admin
- **Response:**
```json
{ "session_expiry_hours": 720, "is_auth_enabled": true }
```

#### `PUT /api/auth/settings`
- **Role:** admin
- **Request:** `{ "session_expiry_hours": 480 }`

---

### Login (thay đổi)

`POST /api/session/login` — đổi field `username` → `email`:

```json
// Trước
{ "username": "admin", "password": "..." }

// Sau
{ "email": "admin@localhost", "password": "..." }
```

**4 bước xử lý login:**

```
1. Normalize email (lowercase, trim) → query users table WHERE email = ?
2. Nếu không tìm thấy hoặc is_active = false → 401 "invalid email or password"
   (không phân biệt "không tồn tại" vs "sai password" để tránh user enumeration)
3. encrypt.CompareHash(user.password_hash, payload.password)
   → nếu sai → 401 "invalid email or password"
4. Tạo session:
       token     = uuid mới
       user_id   = user.id          ← bắt buộc set
       expires_at = now + session_expiry_hours  ← đọc từ governance_config, KHÔNG hardcode
   UPDATE users SET last_login_at = now WHERE id = user.id
```

Response khi thành công:
```json
{
  "message": "Login successful",
  "user": { "id": "...", "email": "...", "name": "...", "role": "admin" }
}
```

**Breaking change với dashboard UI:** Form login đang gửi field `username`, cần đổi sang `email`.

**`session_expiry_hours` wiring** (xem thêm phần Files cần thay đổi):
- `AuthConfig` struct cần thêm field `SessionExpiryHours int`
- `GetAuthConfig` / `UpdateAuthConfig` trong `rdb.go` đọc/ghi key `session_expiry_hours` từ `governance_config`
- Login handler đọc `authConfig.SessionExpiryHours` thay vì hardcode `time.Hour * 24 * 30` (hiện tại ở `session.go:142`)

---

## Business Rules & Error Handling

### Guards

**Định nghĩa "admin cuối cùng":** User duy nhất có `role = 'admin'` **và** `is_active = true`. Nếu deactivate hoặc demote người đó, không còn admin nào có thể login.

**Guard phải chạy trong DB transaction với lock** (`SELECT FOR UPDATE` trên Postgres, lock table trên SQLite) để tránh race condition trong môi trường cluster — hai request đồng thời có thể cùng pass guard rồi cùng commit.

| Điều kiện | HTTP | Message |
|-----------|------|---------|
| Email đã tồn tại | `409` | "email already in use" |
| Deactivate admin cuối cùng | `400` | "cannot deactivate the last admin" |
| Deactivate chính mình | `400` | "cannot deactivate yourself" |
| Đổi role của chính mình | `400` | "cannot change your own role" |
| Demote admin cuối cùng | `400` | "cannot remove the last admin" |
| Sai `current_password` | `401` | "current password is incorrect" |
| Password < 8 ký tự | `400` | "password must be at least 8 characters" |
| Role không hợp lệ | `400` | "role must be one of: admin, operator, viewer" |

### Email normalization

- Normalize về lowercase trước khi lưu và so sánh
- Trim whitespace
- Validation tối giản: phải có `@` và không rỗng

### Password hashing

- Dùng `encrypt.Hash()` (bcrypt `DefaultCost = 10`) — nhất quán với codebase
- Migration copy thẳng hash có sẵn, KHÔNG gọi `encrypt.Hash()` lần nữa
- `password_hash` không bao giờ xuất hiện trong bất kỳ JSON response nào

### Response format

Follow đúng pattern `SendJSON` / `SendError` hiện tại:
```json
{ "error": "message rõ ràng" }
```

---

## Files cần thay đổi

| File | Loại thay đổi |
|------|--------------|
| `framework/configstore/tables/users.go` | Tạo mới — `TableUser` struct |
| `framework/configstore/tables/sessions.go` | Thêm field `UserID *string` (nullable varchar, no FK tag) |
| `framework/configstore/migrations.go` | Thêm migration mới (dialect-aware, 2 nhánh) |
| `framework/configstore/store.go` | Thêm user CRUD vào `ConfigStore` interface; thêm `SessionExpiryHours int` vào `AuthConfig` struct |
| `framework/configstore/rdb.go` | Implement user CRUD (last-admin guard với tx lock); cập nhật `GetAuthConfig`/`UpdateAuthConfig` để đọc/ghi `session_expiry_hours` |
| `framework/configstore/publishing_config_store.go` | **Không cần thay đổi** — users đọc fresh từ shared DB, không có in-memory cache cần invalidate |
| `transports/bifrost-http/handlers/middlewares.go` | Refactor `validateSession` → trả về `(*TableUser, error)`; gắn user vào request context ở tất cả call-site |
| `transports/bifrost-http/handlers/session.go` | Login dùng email + 4 bước mới; đọc `session_expiry_hours` thay vì hardcode; trả về user info; route `/api/auth/settings` GET+PUT |
| `transports/bifrost-http/handlers/users.go` | Tạo mới — handler cho `/api/users` endpoints |
| `ui/` (login form) | Đổi field `username` → `email` trong form đăng nhập |
