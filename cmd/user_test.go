package cmd

import (
	"fmt"
	"strings"
	"testing"
)

// 回滚全部成功时不应有残留，且删除顺序为：工作目录逆序 → 用户。
func TestRollbackUserAddAllSucceed(t *testing.T) {
	var order []string
	residues := rollbackUserAdd("alice", []string{"/data/alice", "/work/alice"}, true,
		func(dir string) error { order = append(order, "rm "+dir); return nil },
		func(u string) error { order = append(order, "del "+u); return nil })

	if len(residues) != 0 {
		t.Fatalf("期望无残留，实际: %v", residues)
	}
	want := []string{"rm /work/alice", "rm /data/alice", "del alice"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("回滚顺序错误\n期望: %v\n实际: %v", want, order)
	}
}

// 某个工作目录删除失败：残留里点名该目录并给出手动 rm 命令，其余仍继续清理。
func TestRollbackUserAddWorkDirFails(t *testing.T) {
	residues := rollbackUserAdd("bob", []string{"/data/bob", "/work/bob"}, true,
		func(dir string) error {
			if dir == "/data/bob" {
				return fmt.Errorf("device or resource busy")
			}
			return nil
		},
		func(u string) error { return nil })

	if len(residues) != 1 {
		t.Fatalf("期望 1 条残留，实际 %d 条: %v", len(residues), residues)
	}
	if !strings.Contains(residues[0], "/data/bob") || !strings.Contains(residues[0], "rm -rf /data/bob") {
		t.Fatalf("残留信息应点名目录并含手动命令，实际: %q", residues[0])
	}
}

// 用户删除失败：残留里点名用户并给出手动 userdel 命令。
func TestRollbackUserAddDelUserFails(t *testing.T) {
	residues := rollbackUserAdd("carol", nil, true,
		func(dir string) error { return nil },
		func(u string) error { return fmt.Errorf("user carol is currently used by process 123") })

	if len(residues) != 1 {
		t.Fatalf("期望 1 条残留，实际 %d 条: %v", len(residues), residues)
	}
	if !strings.Contains(residues[0], "carol") || !strings.Contains(residues[0], "userdel -r carol") {
		t.Fatalf("残留信息应点名用户并含手动命令，实际: %q", residues[0])
	}
}

// useradd 尚未成功（userCreated=false）时不应调用 delUser，只清理已建的工作目录。
func TestRollbackUserAddUserNotCreated(t *testing.T) {
	delCalled := false
	residues := rollbackUserAdd("dave", []string{"/data/dave"}, false,
		func(dir string) error { return nil },
		func(u string) error { delCalled = true; return nil })

	if delCalled {
		t.Fatal("userCreated=false 时不应调用 delUser")
	}
	if len(residues) != 0 {
		t.Fatalf("期望无残留，实际: %v", residues)
	}
}

// 目录删除与用户删除同时失败：两条残留都应列出，回滚不因前一步失败而中断。
func TestRollbackUserAddBothFail(t *testing.T) {
	residues := rollbackUserAdd("eve", []string{"/data/eve"}, true,
		func(dir string) error { return fmt.Errorf("busy") },
		func(u string) error { return fmt.Errorf("in use") })

	if len(residues) != 2 {
		t.Fatalf("期望 2 条残留，实际 %d 条: %v", len(residues), residues)
	}
}
