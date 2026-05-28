# Spec: Multi-user System (Phase 1 — Spec 1/2)

**Date:** 2026-05-28
**Phase:** 1 — Identity Foundation
**Scope:** Users table, session linking, migration from single-admin, user management API, auth settings API.
**Out of scope:** RBAC permission enforcement (Spec 2), SSO (Phase 4), SCIM (Phase 5).

---

## Context

Bifrost hiện có một admin duy nhất: `admin_username` / `admin_password` lưu trong bảng `governance_config` (key-value). Sessions không liên kết với user nào. Spec này thay thế mô hình đó bằng một users table thực sự, đồng thời giữ nguyên trải nghiệm của admin hiện tại — không ai bị đăng xuất sau upgrade.

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

`password_hash` là nullable ngay từ đầu để schema không cần thay đổi khi SSO được thêm vào Phase 4. `role` lưu dạng string nhất quán với các bảng khác trong codebase.

### Thay đổi bảng `sessions`

Thêm một cột:

```
user_id  VARCHAR(255)  NOT NULL  FK → users(id)
```

`NOT NULL` được vì toàn bộ sessions cũ được backfill về admin user trong migration trước khi constraint được thêm.

---

## Migration

### DB Migration (một migration, một transaction)

Thứ tự thực hiện:

```
1. CREATE TABLE users

2. INSERT INTO users:
       id            = uuid mới
       email         = "admin@localhost"
       name          = governance_config["admin_username"]
       role          = "admin"
       password_hash = bcrypt(governance_config["admin_password"], cost=12)
       is_active     = true

3. ALTER TABLE sessions
   ADD COLUMN user_id VARCHAR(255) NOT NULL DEFAULT '<admin_uuid>'
   REFERENCES users(id)

4. ALTER TABLE sessions
   ALTER COLUMN user_id DROP DEFAULT
```

**Kết quả:** Tất cả sessions hiện tại được gán `user_id = admin_uuid`. Admin không bị đăng xuất sau upgrade.

`admin_username` / `admin_password` trong `governance_config` **không bị xoá** — login handler ngừng đọc chúng nhưng chúng vẫn tồn tại để an toàn nếu cần rollback.

Không có bước Startup Initialization riêng — migration tự đủ.

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

Role không được cache trong session — luôn load fresh từ DB mỗi request. Đảm bảo thay đổi role có hiệu lực ngay lập tức.

### Session invalidation rules

| Sự kiện | Hành động |
|---------|-----------|
| User deactivate (`is_active = false`) | Không xoá session — request kế tiếp tự `401` |
| User soft-delete | Không xoá session — `is_active = false` xử lý |
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

## API

### User Management

#### `GET /api/users`
- **Role:** admin
- **Response:** danh sách users, không bao gồm `password_hash`

```json
{
  "users": [
    {
      "id": "...",
      "email": "alice@acme.com",
      "name": "Alice",
      "role": "operator",
      "is_active": true,
      "last_login_at": "2026-05-28T10:00:00Z",
      "created_at": "2026-05-01T00:00:00Z"
    }
  ]
}
```

---

#### `POST /api/users`
- **Role:** admin
- **Request:**
```json
{ "email": "alice@acme.com", "name": "Alice", "role": "operator", "password": "..." }
```
- **Response:** user object vừa tạo (không có `password_hash`)
- **Rules:** email unique, password ≥ 8 ký tự, role hợp lệ

---

#### `GET /api/users/me`
- **Role:** mọi role (authenticated)
- **Response:** user object của người đang đăng nhập

---

#### `GET /api/users/:id`
- **Role:** admin hoặc chính user đó
- **Response:** user object

---

#### `PUT /api/users/:id`
- **Role:** admin (mọi user) hoặc chính user đó (chỉ của mình)
- **Request:** `{ "name"?: "...", "email"?: "..." }` — các field là optional
- **Rules:** email mới phải unique

---

#### `PUT /api/users/:id/role`
- **Role:** admin
- **Request:** `{ "role": "operator" }`
- **Rules:**
  - Không được đổi role của chính mình
  - Không được demote admin nếu đó là admin cuối cùng

---

#### `PUT /api/users/:id/password`
- **Role:** admin hoặc chính user đó — behavior khác nhau

| Người gọi | Request body | Hành động sau khi đổi |
|-----------|-------------|----------------------|
| Admin reset password user khác | `{ "new_password": "..." }` | Xoá toàn bộ sessions của user đó |
| User tự đổi password | `{ "current_password": "...", "new_password": "..." }` | Xoá tất cả sessions khác, giữ session hiện tại |

---

#### `PUT /api/users/:id/active`
- **Role:** admin
- **Request:** `{ "active": false }` hoặc `{ "active": true }`
- **Rules:**
  - Không được deactivate chính mình
  - Không được deactivate admin cuối cùng

**Không có `DELETE /api/users/:id`** — users chỉ được deactivate để giữ audit history.

---

### Auth Settings

#### `GET /api/auth/settings`
- **Role:** admin
- **Response:**
```json
{
  "session_expiry_hours": 720,
  "is_auth_enabled": true
}
```

#### `PUT /api/auth/settings`
- **Role:** admin
- **Request:** `{ "session_expiry_hours"?: 480 }`

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

---

## Business Rules & Error Handling

### Guards

"Admin cuối cùng" được định nghĩa là: user duy nhất có `role='admin'` **và** `is_active=true`. Nếu deactivate hoặc demote người đó, hệ thống không còn admin nào có thể login.

| Điều kiện | HTTP | Message |
|-----------|------|---------|
| Email đã tồn tại | `409` | "email already in use" |
| Deactivate admin cuối cùng (`role=admin AND is_active=true`) | `400` | "cannot deactivate the last admin" |
| Deactivate chính mình | `400` | "cannot deactivate yourself" |
| Đổi role của chính mình | `400` | "cannot change your own role" |
| Demote admin cuối cùng (`role=admin AND is_active=true`) | `400` | "cannot remove the last admin" |
| Sai `current_password` | `401` | "current password is incorrect" |
| Password < 8 ký tự | `400` | "password must be at least 8 characters" |
| Role không hợp lệ | `400` | "role must be one of: admin, operator, viewer" |

### Email normalization

- Normalize về lowercase trước khi lưu và so sánh
- Trim whitespace
- Validation tối giản: phải có `@` và không rỗng

### Password security

- bcrypt cost factor `12` — nhất quán với `encrypt` package hiện tại
- `password_hash` không bao giờ xuất hiện trong bất kỳ JSON response nào

### Response format

Follow đúng pattern `SendJSON` / `SendError` hiện tại:
```json
// Error
{ "error": "message rõ ràng" }
```

---

## Files cần thay đổi

| File | Loại thay đổi |
|------|--------------|
| `framework/configstore/tables/` | Thêm `users.go` mới |
| `framework/configstore/tables/sessions.go` | Thêm field `UserID` |
| `framework/configstore/migrations.go` | Thêm migration mới |
| `framework/configstore/store.go` | Thêm user CRUD methods vào interface |
| `framework/configstore/rdb.go` | Implement user CRUD methods |
| `transports/bifrost-http/handlers/session.go` | Đổi login dùng email, load user vào context |
| `transports/bifrost-http/handlers/` | Thêm `users.go` handler mới |
| `transports/bifrost-http/lib/middleware.go` | Load user từ session vào context |
