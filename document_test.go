package collabdoc

import "testing"

// 验收场景 1：并发整段插入不交错，同缝隙按作者编号升序，
// 后续编辑可插入段内；重复与因果缺口不得影响结果。
func TestAcceptanceConcurrentRuns(t *testing.T) {
	d1 := New(1, "ab")
	d2 := New(2, "ab")

	r1 := d1.Insert(1, "甲乙") // 作者1：a甲乙b
	r2 := d2.Insert(1, "XY") // 作者2：aXYb
	r3 := d1.Insert(2, "!")  // 作者1 只看见自己的插入后：a甲!乙b

	if got := d1.Read(); got != "a甲!乙b" {
		t.Fatalf("d1 local = %q", got)
	}

	// 因果缺口：r3 先于其依赖 r1 到达，不得提前显示 "!"。
	d2.Receive(r3)
	d2.Receive(r3) // 重复投递
	if got := d2.Read(); got != "aXYb" {
		t.Fatalf("d2 with missing deps = %q, want %q", got, "aXYb")
	}

	// 依赖补齐后 r3 自动生效；重复投递 r1 无额外效果。
	d2.Receive(r1)
	d2.Receive(r1)
	if got := d2.Read(); got != "a甲!乙XYb" {
		t.Fatalf("d2 = %q, want %q", got, "a甲!乙XYb")
	}

	// d1 接收 r2（含重复），两个副本逐码点一致。
	d1.Receive(r2)
	d1.Receive(r2)
	if got := d1.Read(); got != "a甲!乙XYb" {
		t.Fatalf("d1 = %q, want %q, got %q", got, "a甲!乙XYb", got)
	}
	if d1.Read() != d2.Read() {
		t.Fatalf("replicas diverge: %q vs %q", d1.Read(), d2.Read())
	}
}

// 验收场景 2：删除只作用于生成时看见的字符；
// 并发插入在删除区间内部的字符必须保留；重复删除无额外效果。
func TestAcceptanceDeleteVsConcurrentInsert(t *testing.T) {
	d1 := New(1, "abcd")
	d2 := New(2, "abcd")

	rd := d1.Delete(1, 3)   // 删除 "bc"
	ri := d2.Insert(2, "Z") // 并发在位置 2 插入 "Z"

	d1.Receive(ri)
	d2.Receive(rd)
	d2.Receive(rd) // 重复接收删除，不得删掉 "Z"

	if got := d1.Read(); got != "aZd" {
		t.Fatalf("d1 = %q, want %q", got, "aZd")
	}
	if got := d2.Read(); got != "aZd" {
		t.Fatalf("d2 = %q, want %q", got, "aZd")
	}
}

func TestInsertAtBoundaries(t *testing.T) {
	d := New(0, "中")
	d.Insert(0, "𠀀")  // 补充平面字符，文首插入
	d.Insert(2, "é") // 组合字符，文尾插入
	if got := d.Read(); got != "𠀀中é" {
		t.Fatalf("got %q", got)
	}
	if d.Len() != 4 { // 𠀀 中 e ́（组合符为独立码点）
		t.Fatalf("Len = %d, want 4", d.Len())
	}
}

func TestDeleteEverythingAndReinsert(t *testing.T) {
	d1 := New(0, "hello")
	d2 := New(1, "hello")
	rd := d1.Delete(0, 5)
	ri := d1.Insert(0, "world")
	d2.Receive(rd)
	d2.Receive(ri)
	if got := d2.Read(); got != "world" {
		t.Fatalf("got %q", got)
	}
	// 对已删除字符位置的并发插入仍然保留。
	d3 := New(2, "hello")
	ri2 := d3.Insert(3, "X")
	d3.Receive(rd)
	if got := d3.Read(); got != "X" {
		t.Fatalf("got %q", got)
	}
	d3.Receive(ri)
	if got := d3.Read(); got != "Xworld" && got != "worldX" {
		t.Fatalf("unexpected %q", got)
	}
	_ = ri2
}

func TestEmptyOperations(t *testing.T) {
	d := New(0, "ab")
	rec := d.Insert(1, "")
	d.Receive(rec) // 自己已生效的记录是重复投递
	if got := d.Read(); got != "ab" {
		t.Fatalf("got %q", got)
	}
	rd := d.Delete(1, 1)
	if len(rd.Targets) != 0 || d.Read() != "ab" {
		t.Fatalf("empty delete misbehaved")
	}
}

func TestOutOfRangePanics(t *testing.T) {
	d := New(0, "ab")
	mustPanic := func(f func()) {
		defer func() {
			if recover() == nil {
				t.Fatal("expected panic")
			}
		}()
		f()
	}
	mustPanic(func() { d.Insert(3, "x") })
	mustPanic(func() { d.Delete(1, 3) })
	mustPanic(func() { New(10, "") })
}
