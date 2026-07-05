package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/incuscloud/incus-admin/internal/model"
)

// ProvisioningJobRepo 是 provisioning_jobs / provisioning_job_steps 两表的
// 唯一持久化入口。step 的写入按 (job_id, seq) 唯一约束并发安全。
type ProvisioningJobRepo struct {
	db *sql.DB
}

func NewProvisioningJobRepo(db *sql.DB) *ProvisioningJobRepo {
	return &ProvisioningJobRepo{db: db}
}

// Create 入队一个新 job，状态默认 queued。order_id / vm_id 在调用方已知时一起写入；
// vm.create 通常在 allocate_ip 后才有 vm_id，这里允许 nil 由 SetVMID 后补。
func (r *ProvisioningJobRepo) Create(ctx context.Context, kind string, userID, clusterID int64, orderID, vmID *int64, targetName string) (*model.ProvisioningJob, error) {
	var job model.ProvisioningJob
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO provisioning_jobs (kind, user_id, cluster_id, order_id, vm_id, target_name, status)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id, created_at`,
		kind, userID, clusterID, orderID, vmID, targetName, model.JobStatusQueued,
	).Scan(&job.ID, &job.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert provisioning_job: %w", err)
	}
	job.Kind = kind
	job.UserID = userID
	job.ClusterID = clusterID
	job.OrderID = orderID
	job.VMID = vmID
	job.TargetName = targetName
	job.Status = model.JobStatusQueued
	return &job, nil
}

// MarkRunning 把 queued 的 job 翻成 running 并填 started_at。失败（已是终态）
// 用 WHERE status='queued' 防止 worker 多次启动同一 job。
func (r *ProvisioningJobRepo) MarkRunning(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE provisioning_jobs
		 SET status = $1, started_at = NOW()
		 WHERE id = $2 AND status = $3`,
		model.JobStatusRunning, id, model.JobStatusQueued,
	)
	if err != nil {
		return fmt.Errorf("mark job running: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("job %d not in queued state", id)
	}
	return nil
}

// SetVMID 在 allocate_ip 步骤后把新建的 vm_id 写回 job，便于 SSE/UI 反查。
func (r *ProvisioningJobRepo) SetVMID(ctx context.Context, id, vmID int64) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE provisioning_jobs SET vm_id = $1 WHERE id = $2`, vmID, id,
	)
	return err
}

// Finish 把 job 推到终态。errMsg 为空表示成功。
//
// P1-1：WHERE 加 status IN ('queued','running') 守卫，只允许非终态 → 终态的
// 单向跃迁。防两类竞态：worker 正常 finalize 与 sweeper recoverStale 同时对一
// 行写终态（谁先谁赢，后者 0 行、不覆盖）；以及重复 finish 把 succeeded 覆盖成
// partial。命中 0 行不视为错误（幂等 no-op），补偿路径自身幂等/best-effort。
func (r *ProvisioningJobRepo) Finish(ctx context.Context, id int64, status string, errMsg string) error {
	var errPtr *string
	if errMsg != "" {
		errPtr = &errMsg
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE provisioning_jobs
		 SET status = $1, error = $2, completed_at = NOW()
		 WHERE id = $3 AND status IN ($4, $5)`,
		status, errPtr, id, model.JobStatusQueued, model.JobStatusRunning,
	)
	return err
}

// RefundOnce 在单事务内完成"标记已退款 + 加余额 + 插 transactions"三件事。
// 仅当 refund_done_at IS NULL 时才推进；rows-affected = 0 → 之前已退过，
// 直接返回 (false, nil)。失败任何一步整事务回滚 → 下次 worker / sweeper 重试。
//
// 这是退款的原子契约。调用方不应再单独调 AdjustBalance —— 否则违反幂等。
//
// 历史 bug：v1 是 MarkRefunded → AdjustBalance 两步。AdjustBalance 因 FK 失败时
// refund_done_at 已设，sweep 重试也跳过 → 用户余额永久没退。生产首次失败 case 撞上。
func (r *ProvisioningJobRepo) RefundOnce(ctx context.Context, jobID, userID int64, amount float64, description string) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin refund tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // best-effort

	res, err := tx.ExecContext(ctx,
		`UPDATE provisioning_jobs
		 SET refund_done_at = NOW()
		 WHERE id = $1 AND refund_done_at IS NULL`,
		jobID,
	)
	if err != nil {
		return false, fmt.Errorf("mark refund: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		// 已退过，幂等 no-op
		return false, nil
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE users SET balance = balance + $1, updated_at = NOW() WHERE id = $2`,
		amount, userID,
	); err != nil {
		return false, fmt.Errorf("add balance: %w", err)
	}

	// transactions.created_by FK 指向 users(id)；refund 由系统执行而非 admin shadow，
	// 留 NULL 即可。订单 / job 关联信息已写入 description 里足够审计追溯。
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO transactions (user_id, amount, type, description) VALUES ($1, $2, $3, $4)`,
		userID, amount, "refund", description,
	); err != nil {
		return false, fmt.Errorf("insert refund tx: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit refund: %w", err)
	}
	return true, nil
}

// AppendStep 推一条新 step 行，seq 递增。worker 必须按 seq=0..N 顺序写入；
// UNIQUE(job_id, seq) 在并发场景下避免重复写。返回新 step 用于 SSE 推送。
func (r *ProvisioningJobRepo) AppendStep(ctx context.Context, jobID int64, seq int, name, status, detail string) (*model.ProvisioningJobStep, error) {
	var detailPtr *string
	if detail != "" {
		detailPtr = &detail
	}
	var startedAt sql.NullTime
	if status == model.StepStatusRunning {
		startedAt.Time = time.Now()
		startedAt.Valid = true
	}
	var completedAt sql.NullTime
	if status == model.StepStatusSucceeded || status == model.StepStatusFailed || status == model.StepStatusSkipped {
		completedAt.Time = time.Now()
		completedAt.Valid = true
	}

	step := &model.ProvisioningJobStep{
		JobID:  jobID,
		Seq:    seq,
		Name:   name,
		Status: status,
		Detail: detailPtr,
	}
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO provisioning_job_steps (job_id, seq, name, status, detail, started_at, completed_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id`,
		jobID, seq, name, status, detailPtr, startedAt, completedAt,
	).Scan(&step.ID)
	if err != nil {
		return nil, fmt.Errorf("insert job step: %w", err)
	}
	if startedAt.Valid {
		t := startedAt.Time
		step.StartedAt = &t
	}
	if completedAt.Valid {
		t := completedAt.Time
		step.CompletedAt = &t
	}
	return step, nil
}

// UpdateStep 把已存在的 step 翻成终态并写 detail。worker 在长 step 中先 AppendStep
// (status=running) 再调本方法终结。
func (r *ProvisioningJobRepo) UpdateStep(ctx context.Context, jobID int64, seq int, status, detail string) error {
	var detailPtr *string
	if detail != "" {
		detailPtr = &detail
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE provisioning_job_steps
		 SET status = $1, detail = COALESCE($2, detail), completed_at = NOW()
		 WHERE job_id = $3 AND seq = $4`,
		status, detailPtr, jobID, seq,
	)
	return err
}

// GetByID 拿单 job + 全步骤，给 GET /portal/jobs/{id} 用。
func (r *ProvisioningJobRepo) GetByID(ctx context.Context, id int64) (*model.ProvisioningJob, error) {
	var job model.ProvisioningJob
	err := r.db.QueryRowContext(ctx,
		`SELECT id, kind, user_id, cluster_id, order_id, vm_id, target_name,
		        status, error, refund_done_at, created_at, started_at, completed_at
		 FROM provisioning_jobs WHERE id = $1`, id,
	).Scan(
		&job.ID, &job.Kind, &job.UserID, &job.ClusterID, &job.OrderID, &job.VMID,
		&job.TargetName, &job.Status, &job.Error, &job.RefundDoneAt,
		&job.CreatedAt, &job.StartedAt, &job.CompletedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	steps, err := r.ListSteps(ctx, id, -1)
	if err != nil {
		return nil, err
	}
	job.Steps = steps
	return &job, nil
}

// ListSteps 按 seq 升序返回 job 的所有步骤。afterSeq < 0 表示全量；
// SSE Last-Event-ID 重连时传 last_seq 让服务端从 seq > N 回放。
func (r *ProvisioningJobRepo) ListSteps(ctx context.Context, jobID int64, afterSeq int) ([]model.ProvisioningJobStep, error) {
	q := `SELECT id, job_id, seq, name, status, detail, started_at, completed_at
	      FROM provisioning_job_steps
	      WHERE job_id = $1 AND seq > $2
	      ORDER BY seq ASC`
	rows, err := r.db.QueryContext(ctx, q, jobID, afterSeq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var steps []model.ProvisioningJobStep
	for rows.Next() {
		var s model.ProvisioningJobStep
		if err := rows.Scan(&s.ID, &s.JobID, &s.Seq, &s.Name, &s.Status, &s.Detail, &s.StartedAt, &s.CompletedAt); err != nil {
			return nil, err
		}
		steps = append(steps, s)
	}
	return steps, rows.Err()
}

// FindStaleRunning 列出进入运行态早于 cutoff 仍在 queued/running 的 job。
// worker 启动时调一次（catch crashed jobs），运行期 sweeper 每 5min 调一次。
//
// P1-1：过期判定用 COALESCE(started_at, created_at) 而非 created_at。running job
// 的真实起跑时刻是 started_at；用 created_at 会把"排队很久才起跑、实际刚跑没多
// 久"的 job 误判为过期而中途 sweep 掉。queued job 尚无 started_at，回退 created_at。
func (r *ProvisioningJobRepo) FindStaleRunning(ctx context.Context, cutoff time.Time) ([]model.ProvisioningJob, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, kind, user_id, cluster_id, order_id, vm_id, target_name,
		        status, error, refund_done_at, created_at, started_at, completed_at
		 FROM provisioning_jobs
		 WHERE status IN ($1, $2) AND COALESCE(started_at, created_at) < $3
		 ORDER BY id ASC`,
		model.JobStatusQueued, model.JobStatusRunning, cutoff,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []model.ProvisioningJob
	for rows.Next() {
		var j model.ProvisioningJob
		if err := rows.Scan(
			&j.ID, &j.Kind, &j.UserID, &j.ClusterID, &j.OrderID, &j.VMID,
			&j.TargetName, &j.Status, &j.Error, &j.RefundDoneAt,
			&j.CreatedAt, &j.StartedAt, &j.CompletedAt,
		); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// FindActiveByOrder 给 PayHandler 防重复入队用：用户重点击 pay 时若已有 queued/running job，直接返回不再重新入队。
func (r *ProvisioningJobRepo) FindActiveByOrder(ctx context.Context, orderID int64) (*model.ProvisioningJob, error) {
	var job model.ProvisioningJob
	err := r.db.QueryRowContext(ctx,
		`SELECT id, kind, user_id, cluster_id, order_id, vm_id, target_name,
		        status, error, refund_done_at, created_at, started_at, completed_at
		 FROM provisioning_jobs
		 WHERE order_id = $1 AND status IN ($2, $3)
		 ORDER BY id DESC LIMIT 1`,
		orderID, model.JobStatusQueued, model.JobStatusRunning,
	).Scan(
		&job.ID, &job.Kind, &job.UserID, &job.ClusterID, &job.OrderID, &job.VMID,
		&job.TargetName, &job.Status, &job.Error, &job.RefundDoneAt,
		&job.CreatedAt, &job.StartedAt, &job.CompletedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &job, nil
}
