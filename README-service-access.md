# Distributed Services --- Access & Testing Guide

This README documents the access methods and test endpoints in the
supplied project notes. All HTTP routes below are accessed through the
API Gateway unless explicitly marked as direct gRPC.

## 1. Service access overview

  --------------------------------------------------------------------------------------------
  Component               Access method           Address
  ----------------------- ----------------------- --------------------------------------------
  API Gateway             HTTP/REST               `http://localhost:8000`

  Auth Service            REST through gateway    `http://localhost:8000/api/v1/auth/...`

  Product Service         REST through gateway    `http://localhost:8000/api/v1/products...`

  Product Service         Direct gRPC             `localhost:50051`

  Order Service           REST through gateway    `http://localhost:8000/api/v1/orders...`

  Payment Service         REST through gateway    `http://localhost:8000/api/v1/payments...`

  Notification Worker     Background RabbitMQ     Inspect process/container logs
                          consumer                

  Redis                   CLI                     `redis-cli`

  RabbitMQ                Management UI           `http://localhost:15672`
  --------------------------------------------------------------------------------------------

The supplied notes do not specify internal HTTP ports for Auth, Order or
Payment Service, or PostgreSQL connection credentials. They are not
guessed here.

## 2. API Gateway and Postman setup

Base URL: `http://localhost:8000`

Health check:

``` http
GET {{base_url}}/health
```

Expected: `200 {"status":"ok"}`. No bearer token is required.

Create a Postman environment:

  Variable          Value
  ----------------- ---------------------------------
  `base_url`        `http://localhost:8000`
  `token`           Regular user's access token
  `admin_token`     Admin user's access token
  `refresh_token`   Refresh token
  `product_id`      ID saved after product creation
  `order_id`        ID saved after order creation
  `payment_id`      ID saved after listing payments

Use `Authorization: Bearer {{token}}` for authenticated user requests
and `Authorization: Bearer {{admin_token}}` for admin-only product
operations.

## 3. Auth Service --- REST

Base path: `{{base_url}}/api/v1/auth`

  Operation            Method   Path            Authentication
  -------------------- -------- --------------- ----------------------------------
  Register             POST     `/register`     No bearer token in supplied test
  Login                POST     `/login`        No bearer token
  Profile              GET      `/me`           Bearer access token
  Refresh              POST     `/refresh`      Refresh token in JSON body
  Logout               POST     `/logout`       Refresh token in JSON body
  Logout all devices   POST     `/logout-all`   Bearer access token

### Register

``` http
POST {{base_url}}/api/v1/auth/register
Content-Type: application/json
```

``` json
{
  "name": "Test User",
  "email": "testuser@example.com",
  "password": "Password123"
}
```

Expected: `201 Created`, user details and default role `user`.

### Login

``` http
POST {{base_url}}/api/v1/auth/login
Content-Type: application/json
```

``` json
{
  "email": "testuser@example.com",
  "password": "Password123"
}
```

Expected: `200 OK` with `access_token`, `refresh_token`, `token_type`,
`expires_in` and user.

Postman Tests script to save tokens:

``` javascript
let res = pm.response.json();
if (res.access_token) {
    pm.environment.set("token", res.access_token);
    pm.environment.set("refresh_token", res.refresh_token);
}
```

For the admin login, save the returned access token to `admin_token`.

### Get profile

``` http
GET {{base_url}}/api/v1/auth/me
Authorization: Bearer {{token}}
```

Expected: `200 OK` with user ID, name, email and role.

### Refresh tokens

``` http
POST {{base_url}}/api/v1/auth/refresh
Content-Type: application/json
```

``` json
{
  "refresh_token": "{{refresh_token}}"
}
```

Expected: `200 OK` with new tokens. Refresh-token rotation means the old
refresh token becomes invalid. Reuse the token-saving Tests script
above.

### Logout

``` http
POST {{base_url}}/api/v1/auth/logout
Content-Type: application/json
```

``` json
{
  "refresh_token": "{{refresh_token}}"
}
```

Expected: `200 OK`, logout success message.

### Logout all devices

``` http
POST {{base_url}}/api/v1/auth/logout-all
Authorization: Bearer {{token}}
```

No body. Expected: `200 OK`.

## 4. Product Service --- REST through gateway

Base path: `{{base_url}}/api/v1/products`

  ---------------------------------------------------------------------------------------
  Operation         Method            Path                              Access
  ----------------- ----------------- --------------------------------- -----------------
  Create            POST              `/api/v1/products`                Admin

  List              GET               `/api/v1/products`                Authenticated
                                                                        user

  Get by ID         GET               `/api/v1/products/{product_id}`   Authenticated
                                                                        user

  Update            PUT               `/api/v1/products/{product_id}`   Admin

  Delete            DELETE            `/api/v1/products/{product_id}`   Admin
  ---------------------------------------------------------------------------------------

### Create product

``` http
POST {{base_url}}/api/v1/products
Authorization: Bearer {{admin_token}}
Content-Type: application/json
```

``` json
{
  "name": "Wireless Mouse",
  "description": "Logitech MX Master 3",
  "price": 7999.00,
  "stock": 50,
  "category": "electronics"
}
```

Expected: `201 Created`. Save the returned ID:

``` javascript
let res = pm.response.json();
if (res.id) {
    pm.environment.set("product_id", res.id.toString());
}
```

### List products

``` http
GET {{base_url}}/api/v1/products
Authorization: Bearer {{token}}
```

Expected: `200 OK` with product array. Inspect `X-Cache` for gateway
cache status.

### Get product

``` http
GET {{base_url}}/api/v1/products/{{product_id}}
Authorization: Bearer {{token}}
```

### Update product

``` http
PUT {{base_url}}/api/v1/products/{{product_id}}
Authorization: Bearer {{admin_token}}
Content-Type: application/json
```

``` json
{
  "name": "Wireless Mouse Pro",
  "description": "Logitech MX Master 3S",
  "price": 8999.00,
  "stock": 45,
  "category": "electronics"
}
```

Expected: `200 OK`. The supplied notes say this invalidates gateway and
Product Service cache entries.

### Delete product

``` http
DELETE {{base_url}}/api/v1/products/{{product_id}}
Authorization: Bearer {{admin_token}}
```

Expected: `204 No Content`.

## 5. Product Service --- direct gRPC

**Server:** `localhost:50051`

In Postman, create a **gRPC Request**, enter `localhost:50051`, and use
Server Reflection to discover methods.

**Service:** `product.v1.ProductService`

### GetProduct

Method: `GetProduct`

``` json
{
  "product_id": 1
}
```

Successful response contains a `product` object with `id`, `name`,
`price_minor`, `currency`, `stock`, `category` and `description`.
Missing product returns gRPC `NOT_FOUND`.

### ReserveStock

Method: `ReserveStock`

``` json
{
  "product_id": 1,
  "quantity": 2
}
```

Success example:

``` json
{
  "success": true,
  "remaining_stock": 41,
  "message": "reserved 2 units of product 1"
}
```

Insufficient stock example:

``` json
{
  "success": false,
  "remaining_stock": 0,
  "message": "insufficient stock for product 1: 0 available"
}
```

Missing product returns gRPC `NOT_FOUND`. **This call changes stock**,
so use a test product when repeating it.

## 6. Order Service --- REST through gateway

Base path: `{{base_url}}/api/v1/orders`

  Operation        Method   Path
  ---------------- -------- --------------------------------------
  Create order     POST     `/api/v1/orders`
  List orders      GET      `/api/v1/orders`
  Get order        GET      `/api/v1/orders/{order_id}`
  Cancel order     PATCH    `/api/v1/orders/{order_id}/cancel`
  Complete order   PATCH    `/api/v1/orders/{order_id}/complete`

All listed routes use `Authorization: Bearer {{token}}`.

### Create order

``` http
POST {{base_url}}/api/v1/orders
Authorization: Bearer {{token}}
Content-Type: application/json
```

``` json
{
  "items": [
    { "product_id": 1, "quantity": 2 },
    { "product_id": 2, "quantity": 1 }
  ]
}
```

Expected: `201 Created` with order details. Save its ID:

``` javascript
let res = pm.response.json();
if (res.id) {
    pm.environment.set("order_id", res.id.toString());
}
```

The supplied notes say order creation calls Product Service gRPC
`GetProduct` and `ReserveStock`, writes an outbox event, and the outbox
relay publishes it to RabbitMQ.

### List orders

``` http
GET {{base_url}}/api/v1/orders?limit=10&offset=0
Authorization: Bearer {{token}}
```

### Get order

``` http
GET {{base_url}}/api/v1/orders/{{order_id}}
Authorization: Bearer {{token}}
```

The status may already be `confirmed` if payment has completed
asynchronously.

### Cancel order

``` http
PATCH {{base_url}}/api/v1/orders/{{order_id}}/cancel
Authorization: Bearer {{token}}
Content-Type: application/json
```

``` json
{
  "reason": "Changed my mind"
}
```

Body is optional in the supplied notes. Only pending orders can be
cancelled; confirmed/completed orders return `409 Conflict`.

### Complete order

``` http
PATCH {{base_url}}/api/v1/orders/{{order_id}}/complete
Authorization: Bearer {{token}}
```

No body. Only confirmed orders can be completed.

## 7. Payment Service --- REST through gateway

Base path: `{{base_url}}/api/v1/payments`

  Operation       Method   Path
  --------------- -------- -------------------------------------
  List payments   GET      `/api/v1/payments`
  Get payment     GET      `/api/v1/payments/{payment_id}`
  Pay / retry     POST     `/api/v1/payments/{payment_id}/pay`

All listed routes use `Authorization: Bearer {{token}}`.

### List payments

``` http
GET {{base_url}}/api/v1/payments
Authorization: Bearer {{token}}
```

The supplied notes say the Payment Service consumer automatically
creates a payment after receiving `order.created`.

Optional Postman Tests script:

``` javascript
let res = pm.response.json();
if (Array.isArray(res) && res.length > 0) {
    pm.environment.set("payment_id", res[0].id.toString());
}
```

### Get payment

``` http
GET {{base_url}}/api/v1/payments/{{payment_id}}
Authorization: Bearer {{token}}
```

### Pay / manual retry

``` http
POST {{base_url}}/api/v1/payments/{{payment_id}}/pay
Authorization: Bearer {{token}}
Content-Type: application/json
```

``` json
{
  "method": "card"
}
```

Body is optional; an empty body uses default method `card` in the
supplied notes.

  -----------------------------------------------------------------------
  Scenario                            Expected response
  ----------------------------------- -----------------------------------
  Payment succeeds                    `200 OK`, status `succeeded`

  Already succeeded                   `409`, `payment already succeeded`

  Already processing                  `409`,
                                      `payment is already processing`

  Simulated decline                   Send `{"method":"declined"}`;
                                      `200 OK`, status `failed`,
                                      simulated decline reason
  -----------------------------------------------------------------------

## 8. Notification Worker --- background access

The supplied notes identify the Notification Worker as a RabbitMQ
consumer:

-   Queue: `notification.order.events`
-   Bound routing key: `order.#`
-   Source exchange: `order.events`

No HTTP endpoint is specified for this worker. Inspect its
process/container logs and the RabbitMQ Management UI to verify
consumption.

## 9. Redis --- CLI access and cache testing

Connect:

``` bash
redis-cli
```

The two cache layers share Redis but use separate prefixes:

  Layer                    Key prefix         TTL in supplied notes
  ------------------------ ---------------- -----------------------
  Gateway response cache   `gw:response:`                 5 minutes
  Product Service cache    `product:`                    10 minutes

Inspect keys and values:

``` redis
KEYS *
KEYS gw:response:*
KEYS product:*
GET "gw:response:/api/v1/products"
GET "product:1"
TTL "gw:response:/api/v1/products"
TTL "product:1"
```

Force cache misses by deleting individual keys:

``` redis
DEL "gw:response:/api/v1/products"
DEL "product:1"
```

`FLUSHALL` removes every key in the selected Redis database; use it only
in a disposable local test environment.

### Test gateway response cache

1.  Send `GET {{base_url}}/api/v1/products`.
2.  First request should show `X-Cache: MISS`.
3.  Repeat the request; it should show `X-Cache: HIT`.
4.  Inspect `gw:response:*` keys.
5.  Update a product as admin.
6.  Repeat the GET; supplied notes expect `MISS` after invalidation.

### Test Product Service cache

1.  Call gRPC `GetProduct` for a product; first call should log
    `CACHE MISS`.
2.  Call again; expect `CACHE HIT`.
3.  Inspect `product:{id}` in Redis.
4.  Call gRPC `ReserveStock`; it invalidates the product cache.
5.  Call `GetProduct` again; expect a cache miss and DB fetch.

The two prefixes are isolated: deleting a `gw:response:` key does not
delete a `product:` key.

## 10. RabbitMQ --- Management UI

**URL:** `http://localhost:15672`

The supplied local test credentials are:

  Username   Password
  ---------- ------------
  `admin`    `admin123`

Use these only in a local development environment; do not use these
credentials in production.

### Exchanges

  ------------------------------------------------------------------------------
  Exchange                       Type                    Purpose
  ------------------------------ ----------------------- -----------------------
  `order.events`                 topic                   Order Service publishes
                                                         `order.created`,
                                                         `order.cancelled`,
                                                         `order.completed`

  `payment.events`               topic                   Payment Service
                                                         publishes
                                                         `payment.completed`,
                                                         `payment.failed`

  `order.events.dlx`             topic                   Notification consumer
                                                         dead-letter exchange

  `order.events.retry`           topic                   Notification consumer
                                                         retry exchange

  `payment.order.events.dlx`     topic                   Payment consumer
                                                         dead-letter exchange

  `payment.order.events.retry`   topic                   Payment consumer retry
                                                         exchange

  `order.payment.events.dlx`     topic                   Order Service
                                                         payment-result consumer
                                                         dead-letter exchange

  `order.payment.events.retry`   topic                   Order Service
                                                         payment-result consumer
                                                         retry exchange
  ------------------------------------------------------------------------------

### Queues

  -----------------------------------------------------------------------------
  Queue                         Consumer / role         Binding or behavior
  ----------------------------- ----------------------- -----------------------
  `payment.order.events`        Payment Service         `order.#`

  `notification.order.events`   Notification Worker     `order.#`

  `order.payment.events`        Order Service           `payment.#`

  `order.events.dead`           DLQ; no consumer in     `#`
                                notes                   

  `order.events.retry`          Retry queue             30-second TTL, then
                                                        dead-letter back

  `payment.order.events.dead`   DLQ; no consumer in     `#`
                                notes                   

  `payment.order.retry`         Retry queue             30-second TTL, then
                                                        dead-letter back

  `order.payment.events.dead`   DLQ; no consumer in     `#`
                                notes                   

  `order.payment.retry`         Retry queue             30-second TTL, then
                                                        dead-letter back
  -----------------------------------------------------------------------------

Queue names are reproduced from the supplied notes. Check the current
Management UI if topology has changed.

### What to inspect

-   **Exchanges:** bindings, routing keys and destination queues.
-   **Queues:** Ready, Unacked and Total message counts.
-   **Consumers:** which service is connected to each queue.
-   **Rates:** publish and acknowledgement graphs.
-   **Get messages:** inspect message bodies carefully.

### End-to-end event flow check

1.  Create an order through `POST /api/v1/orders`.
2.  Inspect `payment.order.events`; Payment Service consumes
    `order.created`.
3.  Inspect `notification.order.events`; Notification Worker consumes
    the order event.
4.  Inspect `order.payment.events`; Order Service consumes
    `payment.completed` or `payment.failed`.
5.  Check Order Service logs for the outbox relay publication message.
6.  Retrieve the order and verify its status.

### Retry / DLQ test

1.  Temporarily stop Payment Service.
2.  Create an order.
3.  Observe the message in `payment.order.events` while no consumer is
    available.
4.  Restart Payment Service and verify it consumes the message.
5.  To exercise retry handling, publish a deliberately malformed test
    event or cause a controlled consumer-processing failure.
6.  Observe the retry exchange and retry queue.
7.  Verify that an event exhausting retry handling reaches the payment
    DLQ.

**Important:** Stopping a consumer normally leaves messages Ready in the
original queue; it does not by itself trigger retry. A retry cycle
requires the consumer to reject/fail the message according to the
application's retry implementation. TTL controls delay; it does not
independently enforce a retry count.

### Publish a manual test event

In **Exchanges → `order.events` → Publish message**, use routing key:

``` text
order.created
```

Example body:

``` json
{
  "event_id": "test-manual-001",
  "event_type": "order.created",
  "order_id": 999,
  "user_id": 1,
  "total_amount": 1000,
  "currency": "INR",
  "created_at": "2026-11-04T00:00:00Z"
}
```

**Caution:** The supplied notes warn that this can create a payment
record for order `999` even though that order does not exist in the
Order Service database. Use a disposable test environment and unique
event IDs.

## 11. Admin test-account promotion

The supplied test sequence registers a second user and promotes it to
admin with:

``` sql
UPDATE users
SET role = 'admin'
WHERE email = 'adminuser@example.com';
```

Run this against the Auth Service database using your configured
database connection. The supplied notes do not include PostgreSQL host,
port, username or password.

## 12. Recommended end-to-end test sequence

  -------------------------------------------------------------------------
                     \# Access method    Action           Verification
  --------------------- ---------------- ---------------- -----------------
                      1 HTTP             `GET /health`    Gateway responds

                      2 HTTP             Register regular User created with
                                         user             role `user`

                      3 HTTP + SQL       Register second  Admin role set
                                         user and promote 
                                         to admin         

                      4 HTTP             Log in as        Save `token`,
                                         regular user     `refresh_token`

                      5 HTTP             Log in as admin  Save
                                                          `admin_token`

                      6 HTTP             Create products  Product IDs saved
                                         as admin         

                      7 HTTP             List products    Gateway cache
                                         twice            MISS, then HIT

                      8 gRPC             Call             Product cache
                                         `GetProduct`     MISS, then HIT
                                         twice            

                      9 gRPC             Call             Stock decreases;
                                         `ReserveStock`   product cache
                                                          invalidates

                     10 HTTP             Create order     Order ID saved

                     11 RabbitMQ UI      Inspect payment  Consumers receive
                                         and notification events
                                         queues           

                     12 HTTP             Retrieve order   Verify current
                                                          status

                     13 HTTP             List/retrieve    Payment record
                                         payments         and status
                                                          visible

                     14 HTTP             Update product   Cache
                                         as admin         invalidation
                                                          occurs

                     15 HTTP             List products    Gateway cache
                                         again            MISS

                     16 HTTP             Complete         Order completion
                                         confirmed order  succeeds

                     17 HTTP             Refresh/revoke   Rotation and
                                         tokens           revocation work

                     18 Redis CLI        Inspect both key Cache keys and
                                         prefixes         TTLs visible
  -------------------------------------------------------------------------

## 13. Quick access reference

  -----------------------------------------------------------------------
  Need                                Access
  ----------------------------------- -----------------------------------
  REST APIs                           `http://localhost:8000`

  Health                              `http://localhost:8000/health`

  Auth                                `/api/v1/auth/...`

  Products                            `/api/v1/products...`

  Orders                              `/api/v1/orders...`

  Payments                            `/api/v1/payments...`

  Product gRPC                        `localhost:50051`

  Redis                               `redis-cli`

  RabbitMQ UI                         `http://localhost:15672`

  Notification Worker                 Process/container logs

  PostgreSQL                          Service-specific configured DB
                                      connection; details not present in
                                      supplied notes
  -----------------------------------------------------------------------

## 14. Scope and caveats

-   REST requests in this guide go through the API Gateway.
-   Product Service is the only service with a direct gRPC address
    documented in the supplied notes.
-   Internal HTTP ports and PostgreSQL credentials are not provided
    here.
-   Example IDs, payloads and responses are illustrative; actual IDs and
    asynchronous statuses depend on the running environment.
-   Verify RabbitMQ queue and exchange names against the live Management
    UI if the topology has changed.
