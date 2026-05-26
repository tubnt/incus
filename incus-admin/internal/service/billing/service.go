// Package billing 实现 PLAN-054 / INFRA-013 按周期扣费引擎。
//
// 三条职责：
//   - ChargeDue：扫所有 paid_until <= NOW 的 active 订阅，扣 1 个周期 → 续期 /
//     suspended（余额不足）/ skipped（今日已扣过）。
//   - ExpireGrace：扫所有 suspended 且 grace_until 已过的订阅，trash 关联 VM +
//     订阅置 cancelled。
//   - ReactivateOnTopUp：用户充值后即时检查其 suspended 订阅，余额够则补扣 +
//     ClearSuspension。
//
// 所有 DB 操作单事务 + UNIQUE(sub_id, charge_date) 守住 catchup / 重启 / cron
// 双重触发的幂等性。时区统一 UTC。
package billing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/incuscloud/incus-admin/internal/model"
	"github.com/incuscloud/incus-admin/internal/repository"
)

// AuditWriter 与 worker / jobs 同形态：异步写一条 audit_log。
type AuditWriter interface {
	Log(ctx context.Context, userID *int64, action, targetType string, targetID int64, details map[string]any, ip string)
}

// VMTrasher 让 grace expire 不直接依赖 VMRepo —— 测试可注入 stub。
// 实现端：repository.VMRepo.MarkTrashed。
type VMTrasher interface {
	MarkTrashed(ctx context.Context, vmID int64) (bool, error)
}

// Service 聚合 billing 三条工作流的依赖。
type Service struct {
	db      *sql.DB
	subs    *repository.SubscriptionRepo
	charges *repository.ChargeRepo
	vms     VMTrasher
	audit   AuditWriter
	// now 是可注入时钟。生产传 nil → time.Now().UTC()。测试 freeze-time 用。
	now func() time.Time
	// graceDuration 是 suspended → trash 的宽限期。默认 72h；测试可缩短。
	graceDuration time.Duration
	// listLimit ChargeDue / ExpireGrace 单次扫描批量上限。0 = 不限。生产建议 1000。
	listLimit int
}

// Option 调整 Service 行为。
type Option func(*Service)

// WithClock 注入时钟函数（必须返回 UTC time）。测试 freeze-time 用。
func WithClock(fn func() time.Time) Option {
	return func(s *Service) { s.now = fn }
}

// WithGraceDuration 覆盖默认 72h 宽限期（测试可缩短到秒级）。
func WithGraceDuration(d time.Duration) Option {
	return func(s *Service) {
		if d > 0 {
			s.graceDuration = d
		}
	}
}

// WithListLimit 限制单次 ChargeDue / ExpireGrace 拉取的行数。0 = 不限。
func WithListLimit(n int) Option {
	return func(s *Service) {
		if n > 0 {
			s.listLimit = n
		}
	}
}

// DefaultGraceDuration PLAN-054 §2 H：余额不足后 3 天宽限期。
const DefaultGraceDuration = 72 * time.Hour

// DefaultListLimit ChargeDue / ExpireGrace 单次扫描批量上限。生产建议留默认；
// 1000 行/轮 + 1h tick 顶得住 24k active subs，超出再拉大或缩 tick。
const DefaultListLimit = 1000

// NewService 组装 Service。audit 可为 nil（落到 noopAudit），方便测试。
func NewService(db *sql.DB, subs *repository.SubscriptionRepo, charges *repository.ChargeRepo, vms VMTrasher, audit AuditWriter, opts ...Option) *Service {
	if audit == nil {
		audit = noopAudit{}
	}
	s := &Service{
		db:            db,
		subs:          subs,
		charges:       charges,
		vms:           vms,
		audit:         audit,
		graceDuration: DefaultGraceDuration,
		listLimit:     DefaultListLimit,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *Service) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now().UTC()
}

// ===========================================================================
// ChargeDue
// ===========================================================================

// ChargeStats 单次 ChargeDue 的统计。
type ChargeStats struct {
	Processed int // ListDueActive 返回的总行数
	Paid      int // 扣费成功 + paid_until 推进
	Suspended int // 余额不足 → suspended
	Skipped   int // (sub, today) 已存在（UNIQUE 冲突；cron 重跑 / 启动 catchup）
	Errors    int // 单 sub 处理失败（DB error）；继续处理后续 sub
}

// ChargeDue 扫所有 paid_until <= NOW 的 active 订阅，逐个进行单事务扣费。
//
// 单个 sub 失败仅记 log + Errors++，不阻塞后续；返回的 err 只在 ListDueActive
// 本身失败时非 nil。
func (s *Service) ChargeDue(ctx context.Context) (ChargeStats, error) {
	stats := ChargeStats{}
	now := s.clock()
	due, err := s.subs.ListDueActive(ctx, now, s.listLimit)
	if err != nil {
		return stats, fmt.Errorf("list due: %w", err)
	}
	stats.Processed = len(due)
	for i := range due {
		sub := due[i]
		outcome, err := s.chargeOne(ctx, &sub, now)
		if err != nil {
			slog.Error("billing: charge sub failed", "sub_id", sub.ID, "user_id", sub.UserID, "vm_id", sub.VMID, "error", err)
			stats.Errors++
			continue
		}
		switch outcome {
		case chargeOutcomePaid:
			stats.Paid++
		case chargeOutcomeSuspended:
			stats.Suspended++
		case chargeOutcomeSkipped:
			stats.Skipped++
		}
	}
	return stats, nil
}

type chargeOutcome int

const (
	chargeOutcomeUnknown chargeOutcome = iota
	chargeOutcomePaid
	chargeOutcomeSuspended
	chargeOutcomeSkipped
)

// chargeOne 单 sub 的原子扣费循环：
//
//	BEGIN
//	  SELECT vm_subscriptions FOR UPDATE  -- 与 reactivateOne 串行
//	  IF sub.status != 'active' THEN ROLLBACK; return skipped  -- 已被 reactivate / cancel 抢走
//	  INSERT billing_charges (sub, date, amount, status='insufficient')
//	    ↘ UNIQUE 冲突 → 已扣过 → ROLLBACK + skipped
//	  SELECT users.balance FOR UPDATE
//	  IF balance >= amount THEN
//	    UPDATE users.balance -= amount
//	    INSERT transactions (type='charge', -amount) RETURNING id
//	    UPDATE billing_charges SET status='paid', transaction_id=...
//	    UPDATE vm_subscriptions SET paid_until = MAX(sub.paid_until + period, now + period)
//	  ELSE
//	    (charge 行已经是 insufficient，无需 UPDATE)
//	    UPDATE vm_subscriptions SET status='suspended', suspended_at, grace_until
//	  COMMIT
//
// 失败路径全 rollback，下一轮 worker 重试。
//
// paid_until carry-forward 采用 max(old+period, now+period)：
//   - 正常 (paid_until 刚到期)：old+period == now+period，等价 carry。
//   - 多日 backlog (worker 离线 N 天)：跳到 now+period，恢复正常 cadence；
//     用户白得 N 天服务，但 paid_until 不再永远卡过去（避免每 tick flapping）。
func (s *Service) chargeOne(ctx context.Context, sub *model.VMSubscription, now time.Time) (chargeOutcome, error) {
	amount, err := chargeAmount(sub)
	if err != nil {
		return chargeOutcomeUnknown, err
	}
	period := model.BillingPeriodDuration(sub.Period)
	if period == 0 {
		return chargeOutcomeUnknown, fmt.Errorf("invalid period %q", sub.Period)
	}

	chargeDate := now.UTC().Truncate(24 * time.Hour)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return chargeOutcomeUnknown, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // 已 commit 时 rollback 是 noop

	// Step 0: 锁 sub 行 —— 与 reactivateOne 在同一行上串行，杜绝 charger + topup
	// hook 并发对同一 (sub, today) 双扣 (CR P0 修复)。锁后重读 status：若已被
	// reactivate / cancel 改走，本次扣费让位。paid_until 同时被锁，防止外部漂移。
	var lockedStatus string
	var lockedPaidUntil time.Time
	if err := tx.QueryRowContext(ctx,
		`SELECT status, paid_until FROM vm_subscriptions WHERE id = $1 FOR UPDATE`,
		sub.ID,
	).Scan(&lockedStatus, &lockedPaidUntil); err != nil {
		return chargeOutcomeUnknown, fmt.Errorf("lock sub: %w", err)
	}
	if lockedStatus != model.SubscriptionStatusActive {
		return chargeOutcomeSkipped, nil
	}
	// 取锁后的最新 paid_until 做 carry-forward 计算，避免基于陈旧的 sub.PaidUntil。
	currentPaidUntil := lockedPaidUntil

	// Step 1: INSERT charge (status=insufficient 占位). UNIQUE 冲突 → 已扣过。
	var chargeID int64
	err = tx.QueryRowContext(ctx,
		`INSERT INTO billing_charges (subscription_id, charge_date, amount, status)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id`,
		sub.ID, chargeDate, amount, model.BillingChargeInsufficient,
	).Scan(&chargeID)
	if err != nil {
		if isUniqueViolation(err) {
			return chargeOutcomeSkipped, nil
		}
		return chargeOutcomeUnknown, fmt.Errorf("insert charge: %w", err)
	}

	// Step 2: SELECT balance FOR UPDATE
	var balance float64
	if err := tx.QueryRowContext(ctx,
		`SELECT balance FROM users WHERE id = $1 FOR UPDATE`, sub.UserID,
	).Scan(&balance); err != nil {
		return chargeOutcomeUnknown, fmt.Errorf("lock user: %w", err)
	}

	if balance >= amount {
		// Step 3a: 扣款 + 交易流水 + UpdatePaid + UpdatePaidUntil
		if _, err := tx.ExecContext(ctx,
			`UPDATE users SET balance = balance - $1, updated_at = NOW() WHERE id = $2`,
			amount, sub.UserID,
		); err != nil {
			return chargeOutcomeUnknown, fmt.Errorf("deduct balance: %w", err)
		}
		var txID int64
		if err := tx.QueryRowContext(ctx,
			`INSERT INTO transactions (user_id, amount, type, description)
			 VALUES ($1, $2, $3, $4) RETURNING id`,
			sub.UserID, -amount, "charge",
			fmt.Sprintf("订阅 #%d %s 扣费", sub.ID, sub.Period),
		).Scan(&txID); err != nil {
			return chargeOutcomeUnknown, fmt.Errorf("insert transaction: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE billing_charges SET status = $1, transaction_id = $2 WHERE id = $3`,
			model.BillingChargePaid, txID, chargeID,
		); err != nil {
			return chargeOutcomeUnknown, fmt.Errorf("mark charge paid: %w", err)
		}
		// paid_until 推一个周期。carry-forward = old + period 是连续 active 的
		// 正常路径；max(., now+period) 保护多日 backlog 不让 paid_until 永远卡过去
		// (CR P1 修复)。
		newPaidUntil := currentPaidUntil.Add(period)
		if floor := now.Add(period); newPaidUntil.Before(floor) {
			newPaidUntil = floor
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE vm_subscriptions SET paid_until = $1, updated_at = NOW() WHERE id = $2`,
			newPaidUntil, sub.ID,
		); err != nil {
			return chargeOutcomeUnknown, fmt.Errorf("update paid_until: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return chargeOutcomeUnknown, fmt.Errorf("commit paid: %w", err)
		}
		// audit 走后置 best-effort（事务已 commit）
		s.audit.Log(ctx, ptrInt64(sub.UserID), "subscription_charged", "subscription", sub.ID, map[string]any{
			"vm_id":          sub.VMID,
			"amount":         amount,
			"period":         sub.Period,
			"new_paid_until": newPaidUntil,
			"transaction_id": txID,
		}, "")
		return chargeOutcomePaid, nil
	}

	// Step 3b: 余额不足 → suspended + grace_until = NOW + 3d
	graceUntil := now.Add(s.graceDuration)
	if _, err := tx.ExecContext(ctx,
		`UPDATE vm_subscriptions
		 SET status = 'suspended', suspended_at = $1, grace_until = $2, updated_at = NOW()
		 WHERE id = $3`,
		now, graceUntil, sub.ID,
	); err != nil {
		return chargeOutcomeUnknown, fmt.Errorf("mark suspended: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return chargeOutcomeUnknown, fmt.Errorf("commit suspended: %w", err)
	}
	s.audit.Log(ctx, ptrInt64(sub.UserID), "subscription_suspended", "subscription", sub.ID, map[string]any{
		"vm_id":       sub.VMID,
		"amount":      amount,
		"balance":     balance,
		"grace_until": graceUntil,
	}, "")
	return chargeOutcomeSuspended, nil
}

// ===========================================================================
// ExpireGrace
// ===========================================================================

// GraceStats 单次 ExpireGrace 的统计。
type GraceStats struct {
	Processed int
	Trashed   int
	Errors    int
}

// ExpireGrace 扫所有 grace_until < NOW 的 suspended 订阅，trash 关联 VM +
// sub 置 cancelled。VM trash 由后续 worker.RunVMTrashPurger 真正 hard-delete。
func (s *Service) ExpireGrace(ctx context.Context) (GraceStats, error) {
	stats := GraceStats{}
	now := s.clock()
	expired, err := s.subs.ListSuspendedExpired(ctx, now, s.listLimit)
	if err != nil {
		return stats, fmt.Errorf("list suspended expired: %w", err)
	}
	stats.Processed = len(expired)
	for i := range expired {
		sub := expired[i]
		if err := s.expireOne(ctx, &sub); err != nil {
			slog.Error("billing: expire grace failed", "sub_id", sub.ID, "vm_id", sub.VMID, "error", err)
			stats.Errors++
			continue
		}
		stats.Trashed++
	}
	return stats, nil
}

func (s *Service) expireOne(ctx context.Context, sub *model.VMSubscription) error {
	// VM trash：MarkTrashed 幂等（已 trash / deleted 行返 false, nil）。CR P2
	// 修复：MarkTrashed 失败 → 返 err，sub 不 cancel；下个 tick 重试。否则若
	// trash 永远失败（Incus 长时间不可用），sub 一旦 cancelled，扫描循环再也
	// 找不到这一行 → VM 永久在跑而不付费。
	if s.vms != nil {
		if _, err := s.vms.MarkTrashed(ctx, sub.VMID); err != nil {
			return fmt.Errorf("vm mark trashed: %w", err)
		}
	}
	if err := s.subs.UpdateStatus(ctx, sub.ID, model.SubscriptionStatusCancelled); err != nil {
		return fmt.Errorf("cancel sub: %w", err)
	}
	s.audit.Log(ctx, ptrInt64(sub.UserID), "subscription_grace_expired", "subscription", sub.ID, map[string]any{
		"vm_id":       sub.VMID,
		"grace_until": sub.GraceUntil,
	}, "")
	s.audit.Log(ctx, ptrInt64(sub.UserID), "vm_auto_trashed", "vm", sub.VMID, map[string]any{
		"reason": "grace_expired",
		"sub_id": sub.ID,
	}, "")
	return nil
}

// ===========================================================================
// ReactivateOnTopUp
// ===========================================================================

// ReactivateStats 单次 ReactivateOnTopUp 的统计。
type ReactivateStats struct {
	Suspended   int // 该用户 suspended 订阅总数
	Reactivated int // 成功补扣 + 解挂数量
	Errors      int
}

// ReactivateOnTopUp 用户充值后调用：扫该用户所有 suspended 订阅，余额够则尝试
// 补扣 + ClearSuspension。从 grace_until ASC 顺序处理（先救最快被 trash 的）。
//
// 失败仅记 log；本方法不该返 err 给调用方（topup 主路径已 commit，hook 失败
// 不应回滚）。
func (s *Service) ReactivateOnTopUp(ctx context.Context, userID int64) (ReactivateStats, error) {
	stats := ReactivateStats{}
	suspended, err := s.subs.ListSuspendedByUser(ctx, userID)
	if err != nil {
		return stats, fmt.Errorf("list suspended: %w", err)
	}
	stats.Suspended = len(suspended)
	now := s.clock()
	for i := range suspended {
		sub := suspended[i]
		ok, err := s.reactivateOne(ctx, &sub, now)
		if err != nil {
			slog.Error("billing: reactivate sub failed", "sub_id", sub.ID, "user_id", sub.UserID, "vm_id", sub.VMID, "error", err)
			stats.Errors++
			continue
		}
		if ok {
			stats.Reactivated++
		}
	}
	return stats, nil
}

// reactivateOne 单事务把一条 suspended sub 拉回 active：
//
//	BEGIN
//	  SELECT vm_subscriptions FOR UPDATE  -- 与 chargeOne 串行
//	  IF sub.status != 'suspended' THEN ROLLBACK; return false （已被 cancel / 自愈）
//	  SELECT users.balance FOR UPDATE
//	  IF balance < amount THEN ROLLBACK; return false （余额不够，留 suspended）
//	  UPDATE users.balance -= amount
//	  INSERT transactions (type='charge', -amount)
//	  UPDATE billing_charges (latest insufficient for sub) SET status=paid, transaction_id
//	     —— 找不到 → 让位（不插 today 行，避免覆写 charger 同日 paid 行）
//	  UPDATE vm_subscriptions SET status='active', paid_until=NOW+period,
//	     suspended_at=NULL, grace_until=NULL
//	  COMMIT
//
// 返回 (true, nil) = 成功补扣；(false, nil) = 余额不足 / sub 已非 suspended /
// 找不到 insufficient charge；err = DB 错误。
func (s *Service) reactivateOne(ctx context.Context, sub *model.VMSubscription, now time.Time) (bool, error) {
	amount, err := chargeAmount(sub)
	if err != nil {
		return false, err
	}
	period := model.BillingPeriodDuration(sub.Period)
	if period == 0 {
		return false, fmt.Errorf("invalid period %q", sub.Period)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Step 0: 锁 sub 行 (CR P0 修复)。若并发被另一路径改成 active / cancelled，
	// 让位即可。锁的存在保证 charger 与 reactivate 不会同时进入 user balance 操作。
	var lockedStatus string
	if err := tx.QueryRowContext(ctx,
		`SELECT status FROM vm_subscriptions WHERE id = $1 FOR UPDATE`, sub.ID,
	).Scan(&lockedStatus); err != nil {
		return false, fmt.Errorf("lock sub: %w", err)
	}
	if lockedStatus != model.SubscriptionStatusSuspended {
		return false, nil
	}

	var balance float64
	if err := tx.QueryRowContext(ctx,
		`SELECT balance FROM users WHERE id = $1 FOR UPDATE`, sub.UserID,
	).Scan(&balance); err != nil {
		return false, fmt.Errorf("lock user: %w", err)
	}
	if balance < amount {
		return false, nil
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE users SET balance = balance - $1, updated_at = NOW() WHERE id = $2`,
		amount, sub.UserID,
	); err != nil {
		return false, fmt.Errorf("deduct balance: %w", err)
	}
	var txID int64
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO transactions (user_id, amount, type, description)
		 VALUES ($1, $2, $3, $4) RETURNING id`,
		sub.UserID, -amount, "charge",
		fmt.Sprintf("订阅 #%d %s 充值恢复扣费", sub.ID, sub.Period),
	).Scan(&txID); err != nil {
		return false, fmt.Errorf("insert transaction: %w", err)
	}
	// 把最近一条 insufficient charge 翻 paid。该行通常是 charger 触发 suspended
	// 时插的占位。CR P0：找不到时不再 INSERT today 行 —— charger 可能并发抢占
	// today 槽位写 paid 行，reactivate 若 ON CONFLICT DO UPDATE 会覆写 charger
	// 的 transaction_id 造成"双扣 + 一条流水悬空"。让位（return false）让用户
	// 下个 worker tick / 充值后再来一次。
	res, err := tx.ExecContext(ctx,
		`UPDATE billing_charges SET status = $1, transaction_id = $2
		 WHERE id = (
		   SELECT id FROM billing_charges
		   WHERE subscription_id = $3 AND status = $4
		   ORDER BY id DESC LIMIT 1
		 )`,
		model.BillingChargePaid, txID, sub.ID, model.BillingChargeInsufficient,
	)
	if err != nil {
		return false, fmt.Errorf("mark charge paid: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// 无 insufficient 行：极少 case（charger 已并发 paid，或 sub 状态被人工
		// 改成 suspended 但没有对应 insufficient 占位）。让事务 rollback，避免
		// 把今天的 paid charge 行被本路径覆写。返 (false, nil) 让调用方 / 下轮
		// worker 自然重试。
		return false, nil
	}
	// 解挂 + paid_until 重置为 NOW + period。原 paid_until 已是过去，从 NOW
	// 重新算一个完整周期是公平做法（用户实际可用时长 = 一个周期）。
	newPaidUntil := now.Add(period)
	if _, err := tx.ExecContext(ctx,
		`UPDATE vm_subscriptions
		 SET status = 'active', suspended_at = NULL, grace_until = NULL,
		     paid_until = $1, updated_at = NOW()
		 WHERE id = $2`,
		newPaidUntil, sub.ID,
	); err != nil {
		return false, fmt.Errorf("clear suspension: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit reactivate: %w", err)
	}
	s.audit.Log(ctx, ptrInt64(sub.UserID), "subscription_reactivated", "subscription", sub.ID, map[string]any{
		"vm_id":          sub.VMID,
		"amount":         amount,
		"period":         sub.Period,
		"new_paid_until": newPaidUntil,
		"transaction_id": txID,
	}, "")
	return true, nil
}

// ===========================================================================
// helpers
// ===========================================================================

// chargeAmount 按 sub.Period 选 daily_rate 或 monthly_rate；nil/<=0 拒绝。
func chargeAmount(sub *model.VMSubscription) (float64, error) {
	switch sub.Period {
	case model.BillingPeriodDaily:
		if sub.DailyRate == nil || *sub.DailyRate <= 0 {
			return 0, fmt.Errorf("sub %d: daily_rate missing or non-positive", sub.ID)
		}
		return *sub.DailyRate, nil
	case model.BillingPeriodMonthly:
		if sub.MonthlyRate == nil || *sub.MonthlyRate <= 0 {
			return 0, fmt.Errorf("sub %d: monthly_rate missing or non-positive", sub.ID)
		}
		return *sub.MonthlyRate, nil
	default:
		return 0, fmt.Errorf("sub %d: unknown period %q", sub.ID, sub.Period)
	}
}

// isUniqueViolation 与 repository.charge_repo 同源；当 INSERT 因 UNIQUE 约束
// 冲突返 23505 时返 true。worker / service 据此跳过已扣过的 (sub, date)。
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, repository.ErrChargeDuplicate) {
		return true
	}
	s := err.Error()
	return strings.Contains(s, "SQLSTATE 23505") ||
		strings.Contains(s, "unique constraint") ||
		strings.Contains(s, "duplicate key")
}

func ptrInt64(v int64) *int64 { return &v }

type noopAudit struct{}

func (noopAudit) Log(context.Context, *int64, string, string, int64, map[string]any, string) {}
