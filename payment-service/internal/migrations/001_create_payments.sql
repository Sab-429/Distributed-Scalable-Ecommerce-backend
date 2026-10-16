CREATE TABLE payments (
    id BIGSERIAL PRIMARY KEY,

    order_id BIGINT NOT NULL UNIQUE,

    user_id BIGINT NOT NULL,

    amount BIGINT NOT NULL,

    currency VARCHAR(3) NOT NULL DEFAULT 'INR',

    status VARCHAR(30) NOT NULL DEFAULT 'pending',

    method VARCHAR(30) NOT NULL DEFAULT 'card',

    provider_ref VARCHAR(255),

    failure_reason VARCHAR(255),

    paid_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT chk_payments_amount
        CHECK (amount >= 0),

    CONSTRAINT chk_payments_status
        CHECK (
            status IN (
                'pending',
                'processing',
                'succeeded',
                'failed',
                'cancelled'
            )
        )
);

CREATE INDEX idx_payments_user_id
    ON payments(user_id);

CREATE INDEX idx_payments_status
    ON payments(status);

CREATE INDEX idx_payments_created_at
    ON payments(created_at);
