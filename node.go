package docmerge

// node 是序列树（treap）中的一个字符节点。
// 中序遍历顺序即文档顺序；删除以墓碑标记。
type node struct {
	ch      rune
	author  int
	seq     int
	deleted bool

	crdtParent *node // CRDT 意义上的父字符（插入时的左邻）

	left, right, parent *node // treap 结构指针
	prio                uint32
	size                int // 子树节点总数
	vis                 int // 子树中未删除节点数
	walk                int // 最近一次兄弟遍历的 walkID
}

func sizeOf(n *node) int {
	if n == nil {
		return 0
	}
	return n.size
}

func visOf(n *node) int {
	if n == nil {
		return 0
	}
	return n.vis
}

// update 根据子节点重算 size 与 vis。
func update(n *node) {
	n.size = 1 + sizeOf(n.left) + sizeOf(n.right)
	n.vis = visOf(n.left) + visOf(n.right)
	if !n.deleted {
		n.vis++
	}
}

// rotateUp 在插入后按优先级向上旋转，恢复最大堆序。
func rotateUp(n *node) {
	for n.parent != nil && n.parent.prio < n.prio {
		p := n.parent
		g := p.parent
		if p.left == n {
			p.left = n.right
			if n.right != nil {
				n.right.parent = p
			}
			n.right = p
		} else {
			p.right = n.left
			if n.left != nil {
				n.left.parent = p
			}
			n.left = p
		}
		p.parent = n
		n.parent = g
		if g != nil {
			if g.left == p {
				g.left = n
			} else {
				g.right = n
			}
		}
		update(p)
		update(n)
	}
}

// fixUp 从 n 向上到根重算所有祖先的 size 与 vis。
func fixUp(n *node) {
	for x := n; x != nil; x = x.parent {
		update(x)
	}
}

// succ 返回中序后继，没有则返回 nil。
func succ(n *node) *node {
	if n.right != nil {
		n = n.right
		for n.left != nil {
			n = n.left
		}
		return n
	}
	for n.parent != nil && n.parent.right == n {
		n = n.parent
	}
	return n.parent
}

// findByVisible 返回可见序号 k（从 0 计）对应的节点，越界返回 nil。
func findByVisible(root *node, k int) *node {
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
