package collabdoc

// node 是隐式 treap 中的节点，每个节点对应正文中的一个字符（含墓碑）。
// treap 的中序遍历顺序即字符的线性顺序（含已删除字符）。
type node struct {
	ch       rune
	id       CharID
	parentID CharID // RGA 树中的父字符；DocStart 表示挂在虚拟根下
	depth    int32  // RGA 树中的深度，虚拟根的子节点深度为 0
	deleted  bool

	prio uint64 // treap 随机优先级

	left, right, parent *node

	size     int32 // 子树节点总数（含墓碑）
	vis      int32 // 子树中未删除节点数
	minDepth int32 // 子树中的最小 depth
}

// sortsBefore 报告兄弟节点 s 是否应排在新字符 (author, seq) 之前：
// 先按作者编号升序，同作者按字符序号降序（同缝隙后到的同作者插入靠左）。
func sortsBefore(s *node, author uint8, seq uint32) bool {
	if s.id.Author != author {
		return s.id.Author < author
	}
	return s.id.Seq > seq
}

func sizeOf(n *node) int32 {
	if n == nil {
		return 0
	}
	return n.size
}

func visOf(n *node) int32 {
	if n == nil {
		return 0
	}
	return n.vis
}

func minDepthOf(n *node) int32 {
	if n == nil {
		return 1<<31 - 1
	}
	return n.minDepth
}

// update 根据子节点重算 n 的聚合信息。
func update(n *node) {
	n.size = 1 + sizeOf(n.left) + sizeOf(n.right)
	n.vis = visOf(n.left) + visOf(n.right)
	if !n.deleted {
		n.vis++
	}
	n.minDepth = n.depth
	if d := minDepthOf(n.left); d < n.minDepth {
		n.minDepth = d
	}
	if d := minDepthOf(n.right); d < n.minDepth {
		n.minDepth = d
	}
}

// split 按线性下标把 treap 分成 [0,k) 与 [k,...) 两棵，并保持 parent 指针正确。
func split(n *node, k int32) (l, r *node) {
	if n == nil {
		return nil, nil
	}
	if k <= sizeOf(n.left) {
		l, r2 := split(n.left, k)
		n.left = r2
		if r2 != nil {
			r2.parent = n
		}
		update(n)
		n.parent = nil
		if l != nil {
			l.parent = nil
		}
		return l, n
	}
	l2, r := split(n.right, k-sizeOf(n.left)-1)
	n.right = l2
	if l2 != nil {
		l2.parent = n
	}
	update(n)
	n.parent = nil
	if r != nil {
		r.parent = nil
	}
	return n, r
}

// merge 合并两棵 treap（l 中所有节点在 r 之前），并保持 parent 指针正确。
func merge(l, r *node) *node {
	if l == nil {
		return r
	}
	if r == nil {
		return l
	}
	if l.prio > r.prio {
		l.right = merge(l.right, r)
		if l.right != nil {
			l.right.parent = l
		}
		update(l)
		l.parent = nil
		return l
	}
	r.left = merge(l, r.left)
	if r.left != nil {
		r.left.parent = r
	}
	update(r)
	r.parent = nil
	return r
}

// insertAt 把 nn 插入到线性下标 idx 处，返回新根。
func insertAt(root, nn *node, idx int32) *node {
	l, r := split(root, idx)
	nn.left, nn.right = nil, nil
	nn.parent = nil
	update(nn)
	return merge(merge(l, nn), r)
}

// nodeAt 返回线性下标 idx 处的节点（含墓碑）。
func nodeAt(root *node, idx int32) *node {
	n := root
	for n != nil {
		ls := sizeOf(n.left)
		if idx < ls {
			n = n.left
		} else if idx == ls {
			return n
		} else {
			idx -= ls + 1
			n = n.right
		}
	}
	return nil
}

// kthVisible 返回第 k 个未删除节点。
func kthVisible(root *node, k int32) *node {
	n := root
	for n != nil {
		lv := visOf(n.left)
		if k < lv {
			n = n.left
			continue
		}
		k -= lv
		if !n.deleted {
			if k == 0 {
				return n
			}
			k--
		}
		n = n.right
	}
	return nil
}

// indexOf 返回 n 的线性下标。
func indexOf(n *node) int32 {
	idx := sizeOf(n.left)
	for n.parent != nil {
		p := n.parent
		if n == p.right {
			idx += sizeOf(p.left) + 1
		}
		n = p
	}
	return idx
}

// firstDepthLE 返回 [lo, ...) 中第一个 depth <= d 的节点的线性下标，不存在返回 -1。
// base 为 n 子树起始下标，递归时内部使用；外部调用传 root 与 0 见 firstDepthLEFrom。
func firstDepthLE(n *node, base, lo, d int32) int32 {
	if n == nil || n.minDepth > d {
		return -1
	}
	mid := base + sizeOf(n.left)
	if mid > lo {
		if r := firstDepthLE(n.left, base, lo, d); r >= 0 {
			return r
		}
	}
	if mid >= lo && n.depth <= d {
		return mid
	}
	return firstDepthLE(n.right, mid+1, lo, d)
}

// tombstone 把 n 标记为已删除并沿父链更新聚合信息。
func tombstone(n *node) {
	if n.deleted {
		return
	}
	n.deleted = true
	for p := n; p != nil; p = p.parent {
		update(p)
	}
}
