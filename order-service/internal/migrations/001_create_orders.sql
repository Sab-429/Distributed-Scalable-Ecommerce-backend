CREATE TABLE orders (
    id BIGSERIAL PRIMARY KEY,

    user_id BIGINT NOT NULL,

    status VARCHAR(30) NOT NULL DEFAULT 'pending',

    total_amount BIGINT NOT NULL,

    currency VARCHAR(3) NOT NULL DEFAULT 'INR',

    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT chk_orders_total_amount
        CHECK (total_amount >= 0),

    CONSTRAINT chk_orders_status
        CHECK (
            status IN (
                'pending',
                'confirmed',
                'cancelled',
                'completed'
            )
        )
);

CREATE INDEX idx_orders_user_id
    ON orders(user_id);

CREATE INDEX idx_orders_status
    ON orders(status);

CREATE INDEX idx_orders_created_at
    ON orders(created_at);
