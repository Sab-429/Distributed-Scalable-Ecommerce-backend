package telemetry

import (
	"context"
	"database/sql"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/gorm"
)

// dbInstruments are the PostgreSQL client metrics shared by every service.
type dbInstruments struct {
	duration   metric.Float64Histogram
	operations metric.Int64Counter
}

var dbInstr = newInstruments("observability/gorm", func(m metric.Meter) dbInstruments {
	duration, _ := m.Float64Histogram("db.client.operation.duration",
		metric.WithDescription("Duration of database operations"),
		metric.WithUnit("s"),
	)
	operations, _ := m.Int64Counter("db.client.operations",
		metric.WithDescription("Number of database operations by outcome"),
		metric.WithUnit("{operation}"),
	)
	return dbInstruments{duration: duration, operations: operations}
})

type gormSpanKey struct{}
type gormStartKey struct{}

// gormPlugin traces every GORM statement and records db.client.* metrics.
// It is intentionally dependency-free (no third-party GORM otel plugin) so the
// span names, attributes and metric names stay consistent with the rest of the
// services.
type gormPlugin struct {
	dbName string
	tracer trace.Tracer
}

// GORMPlugin instruments a GORM database handle:
//
//	db, err := database.Connect(cfg)
//	_ = db.Use(telemetry.GORMPlugin(cfg.DBName))
//
// It must be called after telemetry.Init so the tracer and meter resolve to
// the real providers.
func GORMPlugin(dbName string) gorm.Plugin {
	return &gormPlugin{dbName: dbName}
}

func (p *gormPlugin) Name() string { return "observability:gorm" }

func (p *gormPlugin) Initialize(db *gorm.DB) error {
	p.tracer = otel.Tracer("observability/gorm")

	specs := []struct {
		kind   string
		before func(*gorm.DB) gormRegistrar
		after  func(*gorm.DB) gormRegistrar
	}{
		{"create", func(db *gorm.DB) gormRegistrar { return db.Callback().Create().Before("gorm:create") },
			func(db *gorm.DB) gormRegistrar { return db.Callback().Create().After("gorm:create") }},
		{"query", func(db *gorm.DB) gormRegistrar { return db.Callback().Query().Before("gorm:query") },
			func(db *gorm.DB) gormRegistrar { return db.Callback().Query().After("gorm:query") }},
		{"update", func(db *gorm.DB) gormRegistrar { return db.Callback().Update().Before("gorm:update") },
			func(db *gorm.DB) gormRegistrar { return db.Callback().Update().After("gorm:update") }},
		{"delete", func(db *gorm.DB) gormRegistrar { return db.Callback().Delete().Before("gorm:delete") },
			func(db *gorm.DB) gormRegistrar { return db.Callback().Delete().After("gorm:delete") }},
		{"row", func(db *gorm.DB) gormRegistrar { return db.Callback().Row().Before("gorm:row") },
			func(db *gorm.DB) gormRegistrar { return db.Callback().Row().After("gorm:row") }},
		{"raw", func(db *gorm.DB) gormRegistrar { return db.Callback().Raw().Before("gorm:raw") },
			func(db *gorm.DB) gormRegistrar { return db.Callback().Raw().After("gorm:raw") }},
	}

	for _, spec := range specs {
		if err := spec.before(db).Register("otel:before_"+spec.kind, p.before(spec.kind)); err != nil {
			return err
		}
		if err := spec.after(db).Register("otel:after_"+spec.kind, p.after()); err != nil {
			return err
		}
	}

	if sqlDB, err := db.DB(); err == nil {
		registerPoolGauge(sqlDB, p.dbName)
	}

	return nil
}

// gormRegistrar is the subset of *gorm.processor used by this plugin; it keeps
// the unexported GORM callback types out of our signatures.
type gormRegistrar interface {
	Register(name string, fn func(tx *gorm.DB)) error
}

func (p *gormPlugin) before(op string) func(*gorm.DB) {
	return func(tx *gorm.DB) {
		ctx := tx.Statement.Context
		if ctx == nil {
			ctx = context.Background()
		}

		attrs := dbAttrs(p.dbName, op, tx)
		spanName := "db." + op
		if tx.Statement.Table != "" {
			spanName += " " + tx.Statement.Table
		}

		ctx, span := p.tracer.Start(ctx, spanName,
			trace.WithSpanKind(trace.SpanKindClient),
			trace.WithAttributes(attrs...),
		)
		ctx = context.WithValue(ctx, gormSpanKey{}, span)
		ctx = context.WithValue(ctx, gormStartKey{}, time.Now())
		ctx = context.WithValue(ctx, gormOpKey{}, op)
		tx.Statement.Context = ctx
	}
}

func (p *gormPlugin) after() func(*gorm.DB) {
	return func(tx *gorm.DB) {
		if tx.Statement == nil {
			return
		}
		ctx := tx.Statement.Context
		span, ok := ctx.Value(gormSpanKey{}).(trace.Span)
		if !ok {
			return
		}
		start, _ := ctx.Value(gormStartKey{}).(time.Time)
		elapsed := time.Since(start)

		op, _ := ctx.Value(gormOpKey{}).(string)
		if op == "" {
			op = "query"
		}

		outcome := "success"
		attrs := dbAttrs(p.dbName, op, tx)
		if tx.Error != nil {
			outcome = "error"
			span.SetStatus(codes.Error, tx.Error.Error())
			span.RecordError(tx.Error)
		}
		attrs = append(attrs, attribute.String("outcome", outcome))

		if tx.Statement.SQL.Len() > 0 {
			span.SetAttributes(attribute.String("db.query.text", tx.Statement.SQL.String()))
		}
		span.SetAttributes(attribute.Int64("db.response.rows_affected", tx.Statement.RowsAffected))

		instr := dbInstr.Get()
		instr.duration.Record(ctx, elapsed.Seconds(), metric.WithAttributes(attrs...))
		instr.operations.Add(ctx, 1, metric.WithAttributes(attrs...))

		span.End()
	}
}

type gormOpKey struct{}

func dbAttrs(dbName, op string, tx *gorm.DB) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("db.system.name", "postgresql"),
		attribute.String("db.namespace", dbName),
		attribute.String("db.operation.name", op),
	}
	if tx != nil && tx.Statement != nil && tx.Statement.Table != "" {
		attrs = append(attrs, attribute.String("db.collection.name", tx.Statement.Table))
	}
	return attrs
}

// registerPoolGauge exposes the connection pool as db.client.connections.usage
// (states: idle, used, max) which powers the "databases" dashboard.
func registerPoolGauge(sqlDB *sql.DB, dbName string) {
	meter := otel.GetMeterProvider().Meter("observability/gorm")

	_, _ = meter.Int64ObservableGauge("db.client.connections.usage",
		metric.WithDescription("Number of database connections in the pool"),
		metric.WithUnit("{connection}"),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			st := sqlDB.Stats()
			base := []attribute.KeyValue{
				attribute.String("db.system.name", "postgresql"),
				attribute.String("db.namespace", dbName),
			}
			o.Observe(int64(st.Idle), metric.WithAttributes(append(base, attribute.String("state", "idle"))...))
			o.Observe(int64(st.InUse), metric.WithAttributes(append(base, attribute.String("state", "used"))...))
			o.Observe(int64(st.MaxOpenConnections), metric.WithAttributes(append(base, attribute.String("state", "max"))...))
			return nil
		}),
	)
}
