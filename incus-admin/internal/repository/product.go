package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/incuscloud/incus-admin/internal/model"
)

type ProductRepo struct {
	db *sql.DB
}

func NewProductRepo(db *sql.DB) *ProductRepo {
	return &ProductRepo{db: db}
}

// 列定义集中维护：所有 SELECT 必须用 productSelectCols，避免 PLAN-054 给
// products 加 price_daily / period_supported 后某个 query 漏写、导致 sqlx 扫描
// 错位（参见 d6aee02 commit 的教训）。Scan 顺序也由 scanProductRow 兜底。
const productSelectCols = `id, name, slug, cpu, memory_mb, disk_gb, bandwidth_tb,
	price_monthly, price_daily, period_supported,
	COALESCE(currency, 'USD'), access, active, sort_order`

// scanProductRow 把一行 productSelectCols 顺序的列扫到 model.Product。
// period_supported 走 pgTextSlice 适配，再转成 []string。
func scanProductRow(row interface{ Scan(...any) error }, p *model.Product) error {
	var periodSupported pgTextSlice
	err := row.Scan(
		&p.ID, &p.Name, &p.Slug, &p.CPU, &p.MemoryMB, &p.DiskGB, &p.BandwidthTB,
		&p.PriceMonthly, &p.PriceDaily, &periodSupported,
		&p.Currency, &p.Access, &p.Active, &p.SortOrder,
	)
	if err != nil {
		return err
	}
	p.PeriodSupported = []string(periodSupported)
	return nil
}

func (r *ProductRepo) ListActive(ctx context.Context) ([]model.Product, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+productSelectCols+`
		 FROM products WHERE active = true ORDER BY sort_order ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var products []model.Product
	for rows.Next() {
		var p model.Product
		if err := scanProductRow(rows, &p); err != nil {
			return nil, err
		}
		products = append(products, p)
	}
	return products, rows.Err()
}

func (r *ProductRepo) ListAll(ctx context.Context) ([]model.Product, error) {
	products, _, err := r.ListPaged(ctx, 0, 0)
	return products, err
}

// ListPaged 返回全部产品的分页结果与过滤后总数。limit<=0 表示不限制。
func (r *ProductRepo) ListPaged(ctx context.Context, limit, offset int) ([]model.Product, int64, error) {
	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM products`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count products: %w", err)
	}

	query := `SELECT ` + productSelectCols + `
		 FROM products ORDER BY sort_order ASC, id ASC`
	args := []any{}
	if limit > 0 {
		query += ` LIMIT $1 OFFSET $2`
		args = append(args, limit, offset)
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	products := make([]model.Product, 0)
	for rows.Next() {
		var p model.Product
		if err := scanProductRow(rows, &p); err != nil {
			return nil, 0, err
		}
		products = append(products, p)
	}
	return products, total, rows.Err()
}

func (r *ProductRepo) GetByID(ctx context.Context, id int64) (*model.Product, error) {
	var p model.Product
	row := r.db.QueryRowContext(ctx,
		`SELECT `+productSelectCols+`
		 FROM products WHERE id = $1`, id)
	err := scanProductRow(row, &p)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *ProductRepo) Create(ctx context.Context, p *model.Product) (*model.Product, error) {
	currency := p.Currency
	if currency == "" {
		currency = "USD"
	}
	// period_supported NOT NULL：nil 时退到 DB 默认 ARRAY['monthly']，
	// 显式传 [] 也合法但语义模糊，handler 层应当至少传 ['monthly']。
	periods := pgTextArray(p.PeriodSupported)
	if p.PeriodSupported == nil {
		periods = pgTextArray{model.BillingPeriodMonthly}
	}
	var out model.Product
	row := r.db.QueryRowContext(ctx,
		`INSERT INTO products (name, slug, cpu, memory_mb, disk_gb, bandwidth_tb,
			price_monthly, price_daily, period_supported,
			currency, access, active, sort_order)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		 RETURNING `+productSelectCols,
		p.Name, p.Slug, p.CPU, p.MemoryMB, p.DiskGB, p.BandwidthTB,
		p.PriceMonthly, p.PriceDaily, periods,
		currency, p.Access, p.Active, p.SortOrder,
	)
	if err := scanProductRow(row, &out); err != nil {
		return nil, fmt.Errorf("create product: %w", err)
	}
	return &out, nil
}

func (r *ProductRepo) Update(ctx context.Context, p *model.Product) error {
	currency := p.Currency
	if currency == "" {
		currency = "USD"
	}
	periods := pgTextArray(p.PeriodSupported)
	if p.PeriodSupported == nil {
		periods = pgTextArray{model.BillingPeriodMonthly}
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE products SET name=$1, slug=$2, cpu=$3, memory_mb=$4, disk_gb=$5,
			bandwidth_tb=$6, price_monthly=$7, price_daily=$8, period_supported=$9,
			currency=$10, access=$11, active=$12, sort_order=$13, updated_at=NOW()
		 WHERE id=$14`,
		p.Name, p.Slug, p.CPU, p.MemoryMB, p.DiskGB, p.BandwidthTB,
		p.PriceMonthly, p.PriceDaily, periods,
		currency, p.Access, p.Active, p.SortOrder, p.ID,
	)
	return err
}
