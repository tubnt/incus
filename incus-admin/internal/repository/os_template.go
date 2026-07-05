package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/incuscloud/incus-admin/internal/model"
)

type OSTemplateRepo struct {
	db *sql.DB
}

func NewOSTemplateRepo(db *sql.DB) *OSTemplateRepo {
	return &OSTemplateRepo{db: db}
}

const osTemplateColumns = `id, slug, name, source, protocol, server_url, default_user,
	cloud_init_template, supports_rescue, enabled, sort_order, created_at, updated_at`

func scanOSTemplate(row interface {
	Scan(dest ...any) error
}, t *model.OSTemplate) error {
	return row.Scan(&t.ID, &t.Slug, &t.Name, &t.Source, &t.Protocol, &t.ServerURL,
		&t.DefaultUser, &t.CloudInitTemplate, &t.SupportsRescue, &t.Enabled, &t.SortOrder,
		&t.CreatedAt, &t.UpdatedAt)
}

// ListEnabled returns templates visible to the portal (enabled=true only).
func (r *OSTemplateRepo) ListEnabled(ctx context.Context) ([]model.OSTemplate, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+osTemplateColumns+` FROM os_templates WHERE enabled = true ORDER BY sort_order ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]model.OSTemplate, 0)
	for rows.Next() {
		var t model.OSTemplate
		if err := scanOSTemplate(rows, &t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ListAll returns every row regardless of enabled flag. Used by admin UI.
func (r *OSTemplateRepo) ListAll(ctx context.Context) ([]model.OSTemplate, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+osTemplateColumns+` FROM os_templates ORDER BY sort_order ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]model.OSTemplate, 0)
	for rows.Next() {
		var t model.OSTemplate
		if err := scanOSTemplate(rows, &t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *OSTemplateRepo) GetByID(ctx context.Context, id int64) (*model.OSTemplate, error) {
	var t model.OSTemplate
	err := scanOSTemplate(
		r.db.QueryRowContext(ctx, `SELECT `+osTemplateColumns+` FROM os_templates WHERE id = $1`, id),
		&t,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// GetBySource looks up the template whose source matches the given image alias
// (e.g. "ubuntu/24.04/cloud"). Used by jobs/vm_create.go to pull the
// admin-configured cloud_init_template + default_user for the image the
// user picked. Returns nil if not found (caller falls back to defaults).
//
// OPS-051 / PLAN-052.
func (r *OSTemplateRepo) GetBySource(ctx context.Context, source string) (*model.OSTemplate, error) {
	var t model.OSTemplate
	err := scanOSTemplate(
		r.db.QueryRowContext(ctx,
			`SELECT `+osTemplateColumns+` FROM os_templates WHERE source = $1 ORDER BY enabled DESC, sort_order ASC LIMIT 1`,
			source),
		&t,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *OSTemplateRepo) GetBySlug(ctx context.Context, slug string) (*model.OSTemplate, error) {
	var t model.OSTemplate
	err := scanOSTemplate(
		r.db.QueryRowContext(ctx, `SELECT `+osTemplateColumns+` FROM os_templates WHERE slug = $1`, slug),
		&t,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *OSTemplateRepo) Create(ctx context.Context, t *model.OSTemplate) (*model.OSTemplate, error) {
	protocol := t.Protocol
	if protocol == "" {
		protocol = "simplestreams"
	}
	serverURL := t.ServerURL
	if serverURL == "" {
		serverURL = "https://images.linuxcontainers.org"
	}
	defaultUser := t.DefaultUser
	if defaultUser == "" {
		defaultUser = "ubuntu"
	}

	var out model.OSTemplate
	err := scanOSTemplate(
		r.db.QueryRowContext(ctx,
			`INSERT INTO os_templates (slug, name, source, protocol, server_url, default_user,
				cloud_init_template, supports_rescue, enabled, sort_order)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			 RETURNING `+osTemplateColumns,
			t.Slug, t.Name, t.Source, protocol, serverURL, defaultUser,
			t.CloudInitTemplate, t.SupportsRescue, t.Enabled, t.SortOrder,
		),
		&out,
	)
	if err != nil {
		return nil, fmt.Errorf("create os_template: %w", err)
	}
	return &out, nil
}

func (r *OSTemplateRepo) Update(ctx context.Context, t *model.OSTemplate) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE os_templates SET
			slug = $1, name = $2, source = $3, protocol = $4, server_url = $5, default_user = $6,
			cloud_init_template = $7, supports_rescue = $8, enabled = $9, sort_order = $10,
			updated_at = NOW()
		 WHERE id = $11`,
		t.Slug, t.Name, t.Source, t.Protocol, t.ServerURL, t.DefaultUser,
		t.CloudInitTemplate, t.SupportsRescue, t.Enabled, t.SortOrder, t.ID,
	)
	if err != nil {
		return fmt.Errorf("update os_template: %w", err)
	}
	return nil
}

func (r *OSTemplateRepo) Delete(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM os_templates WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete os_template: %w", err)
	}
	return nil
}
