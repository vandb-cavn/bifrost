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
user_id  VARCHAR(255)  NOT NULL  FK → users(id)
```

`NOT NULL` được vì sessions cũ được backfill về admin user trong cùng migration trước khi constraint được thêm.

---

## Migration

### DB Migration (một migration, một transaction)

Migration phải **dialect-aware** theo đúng pattern hiện tại của codebase (`tx.Dialector.Name() == "sqlite"` / `"postgres"`). Không dùng DDL raw cho bước DROP DEFAULT — SQLite không hỗ trợ `ALTER COLUMN`.

**Thứ tự thực hiện:**

```
1. CREATE TABLE users  (dùng AutoMigrate / CreateTable như các migration khác)

2. Nếu governance_config có admin_username VÀ admin_password:
       INSERT INTO users:
           id            = uuid mới
           email         = "admin@localhost"
           name          = governance_config["admin_username"]
           role          = "admin"
           password_hash = governance_config["admin_password"]  ← copy thẳng, KHÔNG hash lại
           is_active     = true

   Nếu KHÔNG có admin credentials (auth tắt hoặc chưa cấu hình):
       Bỏ qua bước này — users table để rỗng, hệ thống chạy không auth

3. ADD COLUMN sessions.user_id VARCHAR(255) NOT NULL
       DEFAULT '<admin_uuid>'   -- backfill sessions cũ về admin
       REFERENCES users(id)

4. DROP DEFAULT trên sessions.user_id:
       - Postgres: ALTER TABLE sessions ALTER COLUMN user_id DROP DEFAULT
       - SQLite: không cần (SQLite không lưu column default sau ADD COLUMN)
```

**Tại sao copy password_hash mà không hash lại:**
`admin_password` trong `governance_config` đã là bcrypt hash. Hash lần hai sẽ tạo `bcrypt(bcrypt(pw))` — login sau đó sẽ fail. Copy thẳng giá trị đã hash là đúng.

**Khi users table rỗng (không có admin credentials):**
Hệ thống vẫn khởi động bình thường. Không có ai có thể login vào dashboard (đúng với behavior hiện tại khi auth tắt). Admin có thể enable auth và tạo user sau qua config.

`admin_username` / `admin_password` trong `governance_config` **không bị xoá** sau migration — login handler ngừng đọc chúng nhưng chúng vẫn tồn tại để an toàn nếu cần rollback.

**Lưu ý về admin@localhost:**
Email `admin@localhost` là placeholder. Admin có thể cập nhật email thật qua `PUT /api/users/:id` sau khi login. Release notes cần thông báo rõ email này.

---

## Session Behavior

### Authenticated request flow

Mỗi request authenticated thực hiện theo thứ tự:

```
1. Đọc token từ cookie "token" hoặc header "Authorization: Bearer <token>"
2. Tra sessions table → lấy user_id
3. Load user từ users table → lấy role + is_active
4. Nếu is_active = false → 401 Unauthorized
5. Gắn user vào request context để handler và middleware dùng
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

Users là shared state — mutations (`CreateUser`, `UpdateUser`, `DeactivateUser`) phải đi qua `PublishingConfigStore` (hoặc tương đương) để propagate sang các peer nodes, giống các entity khác (virtual keys, teams). Không implement riêng sync protocol — thêm user CRUD methods vào `ConfigStore` interface và wrap trong `PublishingConfigStore` theo đúng pattern hiện tại.

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

Sau khi login thành công, response trả về thêm user info cơ bản:
```json
{
  "message": "Login successful",
  "user": { "id": "...", "email": "...", "name": "...", "role": "admin" }
}
```

**Breaking change với dashboard UI:** Form login của dashboard (file UI) đang gửi field `username`, cần đổi sang `email`. Phải update cả frontend.

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
| `framework/configstore/tables/sessions.go` | Thêm field `UserID` |
| `framework/configstore/migrations.go` | Thêm migration mới (dialect-aware) |
| `framework/configstore/store.go` | Thêm user CRUD methods vào `ConfigStore` interface |
| `framework/configstore/rdb.go` | Implement user CRUD (bao gồm last-admin guard với transaction lock) |
| `framework/configstore/publishing_config_store.go` | Wrap user CRUD methods để cluster sync |
| `transports/bifrost-http/handlers/middlewares.go` | Refactor `validateSession` trả về user info, gắn vào context |
| `transports/bifrost-http/handlers/session.go` | Đổi login dùng email, trả về user info trong response |
| `transports/bifrost-http/handlers/users.go` | Tạo mới — handler cho `/api/users` endpoints |
| `ui/` (login form) | Đổi field `username` → `email` trong form đăng nhập |
