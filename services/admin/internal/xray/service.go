package xray

import (
	"admin/internal/runtime"
	"context"
	"fmt"
	"sync"
)

// Restarter 是 Xray 运行时重启的接缝；生产由 proxy.XrayRestarter 实现，
// 测试可注入 fake。xray 包因此不再 import proxy（解除层反转）。
type Restarter interface {
	RestartXray(ctx context.Context) error
}

type RestarterFunc func(ctx context.Context) error

func (f RestarterFunc) RestartXray(ctx context.Context) error { return f(ctx) }

// Service 编排"改台账 → 渲染配置 → 重启 xray"，并在失败时回滚台账。
type Service struct {
	ledger    *Ledger
	layout    runtime.Layout
	restarter Restarter
	genMu     sync.Mutex
}

func NewService(layout runtime.Layout, notifier runtime.Notifier, restarter Restarter) *Service {
	return &Service{
		ledger:    NewLedger(layout, notifier),
		layout:    layout,
		restarter: restarter,
	}
}

// Ensure 启动引导：加载/规范化台账并渲染配置（不重启，xray 容器随后启动）。
func (s *Service) Ensure() error {
	if err := s.ledger.Ensure(); err != nil {
		return fmt.Errorf("xray ledger: %w", err)
	}
	if _, err := s.render(); err != nil {
		return fmt.Errorf("xray config: %w", err)
	}
	return nil
}

func (s *Service) render() (bool, error) {
	s.genMu.Lock()
	defer s.genMu.Unlock()
	clients, err := s.ledger.Clients()
	if err != nil {
		return false, err
	}
	return Render(s.layout, clients)
}

func (s *Service) restart(ctx context.Context) error {
	if s.restarter == nil {
		return nil
	}
	return s.restarter.RestartXray(ctx)
}

func (s *Service) Clients() ([]Client, error) {
	return s.ledger.Clients()
}

func (s *Service) EmailAliases() map[string]string {
	return s.ledger.Aliases()
}

// Add 新增客户端；渲染/重启失败时回滚台账并恢复旧配置。
func (s *Service) Add(ctx context.Context, entry Client) (Client, error) {
	added, err := s.ledger.Add(entry)
	if err != nil {
		return Client{}, err
	}
	changed, err := s.render()
	if err != nil {
		if _, rbErr := s.ledger.Remove(entry.ID); rbErr != nil {
			return Client{}, fmt.Errorf("%w (rollback failed: %v)", err, rbErr)
		}
		return Client{}, err
	}
	if !changed {
		return added, nil
	}
	if err := s.restart(ctx); err != nil {
		if _, rbErr := s.ledger.Remove(entry.ID); rbErr != nil {
			return Client{}, fmt.Errorf("%w (rollback failed: %v)", ErrRestart, rbErr)
		}
		if _, rbErr := s.render(); rbErr != nil {
			return Client{}, fmt.Errorf("%w (restore failed: %v)", ErrRestart, rbErr)
		}
		return Client{}, fmt.Errorf("%w: %v", ErrRestart, err)
	}
	return added, nil
}

// Delete 删除客户端；渲染/重启失败时写回台账并恢复旧配置。
// 返回被删条目供审计日志使用。
func (s *Service) Delete(ctx context.Context, id, ownerFilter string) (Client, error) {
	deleted, err := s.ledger.Delete(id, ownerFilter)
	if err != nil {
		return Client{}, err
	}
	changed, err := s.render()
	if err != nil {
		if _, rbErr := s.ledger.Add(deleted); rbErr != nil {
			return Client{}, fmt.Errorf("%w (rollback failed: %v)", err, rbErr)
		}
		return Client{}, err
	}
	if !changed {
		return deleted, nil
	}
	if err := s.restart(ctx); err != nil {
		if _, rbErr := s.ledger.Add(deleted); rbErr != nil {
			return Client{}, fmt.Errorf("%w (rollback failed: %v)", ErrRestart, rbErr)
		}
		if _, rbErr := s.render(); rbErr != nil {
			return Client{}, fmt.Errorf("%w (restore failed: %v)", ErrRestart, rbErr)
		}
		return Client{}, fmt.Errorf("%w: %v", ErrRestart, err)
	}
	return deleted, nil
}

// Rename 修改客户端用户名（Xray email）；渲染/重启失败时回滚旧名称并
// 恢复旧配置。返回改名前后条目供流量 alias 与审计使用。
func (s *Service) Rename(ctx context.Context, id, newName, ownerFilter string) (Client, Client, error) {
	old, updated, err := s.ledger.Rename(id, newName, ownerFilter)
	if err != nil {
		return Client{}, Client{}, err
	}
	if old.Name == updated.Name {
		return old, updated, nil
	}
	rollback := func(cause error) error {
		if _, _, rbErr := s.ledger.Rename(id, old.Name, ""); rbErr != nil {
			return fmt.Errorf("%w (rollback failed: %v)", cause, rbErr)
		}
		if _, rbErr := s.render(); rbErr != nil {
			return fmt.Errorf("%w (restore failed: %v)", cause, rbErr)
		}
		return cause
	}

	changed, err := s.render()
	if err != nil {
		return Client{}, Client{}, rollback(err)
	}
	if !changed {
		return old, updated, nil
	}
	if err := s.restart(ctx); err != nil {
		return Client{}, Client{}, rollback(fmt.Errorf("%w: %v", ErrRestart, err))
	}
	return old, updated, nil
}

// DeleteByOwner 删除用户全部客户端并重载；removed==0 时不重启（正常删除
// 无客户端可删）。重试路径由调用方调用 Reload 强制重载。
func (s *Service) DeleteByOwner(ctx context.Context, owner string) (int, error) {
	removed, err := s.ledger.DeleteByOwner(owner)
	if err != nil {
		return 0, err
	}
	if removed == 0 {
		return 0, nil
	}
	changed, err := s.render()
	if err != nil {
		return removed, err
	}
	if changed {
		if err := s.restart(ctx); err != nil {
			return removed, fmt.Errorf("%w: %v", ErrRestart, err)
		}
	}
	return removed, nil
}

// Reload 强制渲染并重启（用于"上一次删除已改台账但 xray 未加载"的重试）。
func (s *Service) Reload(ctx context.Context) error {
	if _, err := s.render(); err != nil {
		return err
	}
	if err := s.restart(ctx); err != nil {
		return fmt.Errorf("%w: %v", ErrRestart, err)
	}
	return nil
}
