package docmerge

import "testing"

// 验收用例 1：并发整段插入不交错，同缝隙按作者升序，因果缺口不提前生效。
func TestConcurrentRunsAndCausalGap(t *testing.T) {
	r1 := NewReplica("ab", 1)
	r2 := NewReplica("ab", 2)
	r3 := NewReplica("ab", 3)

	recA := r1.Insert(1, "甲乙") // 作者1：a甲乙b
	recB := r2.Insert(1, "XY") // 作者2：aXYb（并发）
	recC := r1.Insert(2, "!")  // 作者1 只看见自己的插入后：a甲!乙b

	// 因果缺口：recC 先于其依赖 recA 到达，不得提前显示 "!"。
	r3.Receive(recC)
	if got := r3.Text(); got != "ab" {
		t.Fatalf("缺少依赖时提前生效: %q", got)
	}
	r3.Receive(recA)
	r3.Receive(recA) // 重复投递不得再次生效
	r3.Receive(recB)
	if got := r3.Text(); got != "a甲!乙XYb" {
		t.Fatalf("r3 = %q, want %q", got, "a甲!乙XYb")
	}

	// r2 同样先收到 recC（缺口），再补齐。
	r2.Receive(recC)
	if got := r2.Text(); got != "aXYb" {
		t.Fatalf("r2 缺口期 = %q, want %q", got, "aXYb")
	}
	r2.Receive(recA)
	if got := r2.Text(); got != "a甲!乙XYb" {
		t.Fatalf("r2 = %q, want %q", got, "a甲!乙XYb")
	}

	// r1 收到 r2 的并发插入。
	r1.Receive(recB)
	r1.Receive(recB) // 重复
	if got := r1.Text(); got != "a甲!乙XYb" {
		t.Fatalf("r1 = %q, want %q", got, "a甲!乙XYb")
	}
}

// 验收用例 2：删除只作用于生成时看见的字符，并发插入保留。
func TestDeleteSkipsConcurrentInsert(t *testing.T) {
	ra := NewReplica("abcd", 0)
	rb := NewReplica("abcd", 1)

	del := ra.Delete(1, 3) // 删除 b、c
	ins := rb.Insert(2, "Z")

	rb.Receive(del)
	rb.Receive(del) // 重复接收删除也不能删掉 Z
	if got := rb.Text(); got != "aZd" {
		t.Fatalf("rb = %q, want %q", got, "aZd")
	}

	ra.Receive(ins)
	ra.Receive(ins) // 重复
	if got := ra.Text(); got != "aZd" {
		t.Fatalf("ra = %q, want %q", got, "aZd")
	}
}
