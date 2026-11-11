package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrProfileChanged 表示下载期间配置已被编辑、更新或删除，旧响应不得再提交。
var ErrProfileChanged = errors.New("profile changed while remote update was in progress")

// RemoteProfileUpdate 保存提交前后的独立快照；候选内容不提前覆盖磁盘上的可用配置。
type RemoteProfileUpdate struct {
	root           string
	before, after  Profile
	previous, next string
}

func sameRemoteProfile(a, b Profile) bool {
	return a.ID == b.ID && a.Name == b.Name && a.Type == b.Type &&
		a.RemoteURL == b.RemoteURL && a.AutoUpdate == b.AutoUpdate &&
		a.AutoUpdateInterval == b.AutoUpdateInterval && a.LastUpdated.Equal(b.LastUpdated)
}

// 调用方必须持有 profileMu；路径始终从受管根目录派生，不信任外部 Profile.Path。
func remoteProfileLocked(root, id string) (Profile, string, error) {
	if !profileIDPattern.MatchString(id) {
		return Profile{}, "", ErrProfileChanged
	}
	base, err := profileBase(root)
	if err != nil {
		return Profile{}, "", err
	}
	dir := filepath.Join(base, id)
	info, err := os.Lstat(dir)
	if err != nil {
		return Profile{}, "", errors.Join(ErrProfileChanged, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Profile{}, "", errors.New("unsafe profile directory")
	}
	data, err := readLimited(filepath.Join(dir, "profile.json"), 16<<10)
	if err != nil {
		return Profile{}, "", errors.Join(ErrProfileChanged, err)
	}
	var profile Profile
	if err = json.Unmarshal(data, &profile); err != nil {
		return Profile{}, "", err
	}
	name, err := readLimited(filepath.Join(dir, "name.json"), 8<<10)
	if err != nil {
		return Profile{}, "", err
	}
	if err = json.Unmarshal(name, &profile.Name); err != nil {
		return Profile{}, "", err
	}
	profile.ID, profile.Path = id, filepath.Join(dir, "config.json")
	content, err := ReadProfile(profile)
	return profile, content, err
}

func PrepareRemoteProfileUpdate(root string, expected Profile, content string, updatedAt time.Time) (*RemoteProfileUpdate, error) {
	if expected.Type != "remote" {
		return nil, errors.New("profile is not remote")
	}
	if _, err := parseConfig([]byte(content)); err != nil {
		return nil, err
	}
	profileMu.Lock()
	defer profileMu.Unlock()
	current, previous, err := remoteProfileLocked(root, expected.ID)
	if err != nil {
		return nil, err
	}
	if !sameRemoteProfile(current, expected) {
		return nil, ErrProfileChanged
	}
	next := current
	next.LastUpdated = updatedAt
	return &RemoteProfileUpdate{root: root, before: current, after: next, previous: previous, next: content}, nil
}

func (u *RemoteProfileUpdate) Changed() bool { return u.previous != u.next }

func (u *RemoteProfileUpdate) Commit() (Profile, error) {
	profileMu.Lock()
	defer profileMu.Unlock()
	current, content, err := remoteProfileLocked(u.root, u.before.ID)
	if err != nil {
		return Profile{}, err
	}
	if !sameRemoteProfile(current, u.before) || content != u.previous {
		return Profile{}, ErrProfileChanged
	}
	metadata, err := json.Marshal(u.after)
	if err != nil {
		return Profile{}, err
	}
	if err = atomicWrite(current.Path, []byte(u.next)); err != nil {
		return Profile{}, err
	}
	if err = atomicWrite(filepath.Join(filepath.Dir(current.Path), "profile.json"), metadata); err != nil {
		// 元数据提交失败时恢复旧内容，不能留下半提交的新配置。
		restoreErr := atomicWrite(current.Path, []byte(u.previous))
		return Profile{}, errors.Join(err, restoreErr)
	}
	return u.after, nil
}

// ValidateConfigContent 校验真实启动时使用的派生配置，不开放监听，也不启动核心。
func ValidateConfigContent(ctx context.Context, binary, content string, allowAPIReplace bool) error {
	secret, err := randomID()
	if err != nil {
		return err
	}
	derived, err := deriveConfig([]byte(content), 12345, secret, allowAPIReplace)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "boxpilot-candidate-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "config.json")
	if err = atomicWrite(path, derived); err != nil {
		return err
	}
	output, err := Check(ctx, binary, path)
	if err != nil {
		return fmt.Errorf("validate remote configuration: %w: %s", err, redact(output, secret))
	}
	return nil
}

func (u *RemoteProfileUpdate) Validate(ctx context.Context, binary string, allowAPIReplace bool) error {
	return ValidateConfigContent(ctx, binary, u.next, allowAPIReplace)
}

// Restart 先校验候选，再启动私有候选副本，启动成功后才提交；失败则恢复旧运行副本。
// 显式取消不重新启动核心，以免撤销用户停止或退出的操作。
func (u *RemoteProfileUpdate) Restart(ctx context.Context, manager Controller, binary string, allowAPIReplace bool, cpuSetIDs []uint32) (Profile, error) {
	if err := u.Validate(ctx, binary, allowAPIReplace); err != nil {
		return Profile{}, err
	}
	dir, err := os.MkdirTemp("", "boxpilot-update-*")
	if err != nil {
		return Profile{}, err
	}
	defer os.RemoveAll(dir)
	old, candidate := u.before, u.after
	old.Path, candidate.Path = filepath.Join(dir, "previous.json"), filepath.Join(dir, "candidate.json")
	if err = atomicWrite(old.Path, []byte(u.previous)); err != nil {
		return Profile{}, err
	}
	if err = atomicWrite(candidate.Path, []byte(u.next)); err != nil {
		return Profile{}, err
	}
	// 停止前再核对快照，避免已知过期的下载打断正常服务。
	profileMu.Lock()
	current, content, readErr := remoteProfileLocked(u.root, u.before.ID)
	profileMu.Unlock()
	if readErr != nil {
		return Profile{}, readErr
	}
	if !sameRemoteProfile(current, u.before) || content != u.previous {
		return Profile{}, ErrProfileChanged
	}
	stopCtx, stop := context.WithTimeout(ctx, 8*time.Second)
	err = manager.Stop(stopCtx)
	stop()
	if err != nil {
		return Profile{}, err
	}
	if err = ctx.Err(); err != nil {
		return Profile{}, err
	}
	err = manager.Start(ctx, binary, candidate, allowAPIReplace, cpuSetIDs)
	if err == nil {
		var saved Profile
		if ctx.Err() == nil {
			saved, err = u.Commit()
		} else {
			err = ctx.Err()
		}
		if err == nil {
			return saved, nil
		}
	}
	if ctx.Err() != nil {
		// 取消可能发生在 Start 成功后、提交前；仍须回收尚未提交的候选进程。
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		stopErr := manager.Stop(cleanupCtx)
		cancel()
		return Profile{}, errors.Join(ctx.Err(), stopErr)
	}
	// 恢复不使用已失败的启动上下文，但仍受应用取消和独立超时限制。
	recoveryCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if stopErr := manager.Stop(recoveryCtx); stopErr != nil {
		return Profile{}, errors.Join(err, fmt.Errorf("stop failed candidate: %w", stopErr))
	}
	if restoreErr := manager.Start(recoveryCtx, binary, old, allowAPIReplace, cpuSetIDs); restoreErr != nil {
		return Profile{}, errors.Join(err, fmt.Errorf("restore previous core: %w", restoreErr))
	}
	return Profile{}, fmt.Errorf("remote update failed; previous core restored: %w", err)
}
