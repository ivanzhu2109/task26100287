package collabdoc

// Document 是一个协作文档副本。正文与历史只保存在内存中。
// 调用方需保证同一副本上的调用串行执行。
type Document struct {
	author uint8

	vv [MaxAuthors]uint32 // 各作者已生效记录数

	root *node // 全部字符（含墓碑）的隐式 treap

	initial []*node             // 初始正文字符，下标即 CharID.Seq
	chars   [MaxAuthors][]*node // 各作者插入的字符，下标即 CharID.Seq

	nextRecSeq uint32 // 本作者下一条记录序号
	nextCharID uint32 // 本作者下一个字符序号

	pending   []*Record           // 已收到但依赖未齐的记录
	pendingIn map[recKey]struct{} // pending 去重

	rng uint64 // 伪随机状态，用于 treap 优先级
}

type recKey struct {
	author uint8
	seq    uint32
}

// New 创建一个副本，author 取值 0..9，initial 为所有副本一致的初始正文。
func New(author uint8, initial string) *Document {
	if author >= MaxAuthors {
		panic("collabdoc: author must be in [0, 10)")
	}
	d := &Document{
		author:    author,
		pendingIn: make(map[recKey]struct{}),
		rng:       0x9E3779B97F4A7C15 + uint64(author)*0xD1B54A32D192ED03,
	}
	runes := []rune(initial)
	d.initial = make([]*node, len(runes))
	parent := DocStart
	var depth int32
	for i, ch := range runes {
		id := CharID{Author: initialAuthor, Seq: uint32(i)}
		n := &node{ch: ch, id: id, parentID: parent, depth: depth, prio: d.rand()}
		d.initial[i] = n
		parent = id
		depth++
	}
	d.root = buildTreap(d.initial)
	return d
}

// buildTreap 按给定线性顺序以 O(n) 构建笛卡尔树（最大堆优先级），
// 并自底向上重算聚合信息。
func buildTreap(nodes []*node) *node {
	if len(nodes) == 0 {
		return nil
	}
	stack := make([]*node, 0, 64)
	for _, n := range nodes {
		var last *node
		for len(stack) > 0 && stack[len(stack)-1].prio < n.prio {
			last = stack[len(stack)-1]
			stack = stack[:len(stack)-1]
		}
		n.left = last
		if last != nil {
			last.parent = n
		}
		if len(stack) > 0 {
			top := stack[len(stack)-1]
			top.right = n
			n.parent = top
		}
		stack = append(stack, n)
	}
	root := stack[0]
	root.parent = nil
	// 迭代后序遍历，自底向上重算聚合。
	order := make([]*node, 0, len(nodes))
	work := []*node{root}
	for len(work) > 0 {
		n := work[len(work)-1]
		work = work[:len(work)-1]
		order = append(order, n)
		if n.left != nil {
			work = append(work, n.left)
		}
		if n.right != nil {
			work = append(work, n.right)
		}
	}
	for i := len(order) - 1; i >= 0; i-- {
		update(order[i])
	}
	return root
}

func (d *Document) rand() uint64 {
	d.rng ^= d.rng << 13
	d.rng ^= d.rng >> 7
	d.rng ^= d.rng << 17
	return d.rng
}

// Insert 在可见正文 pos 码点处插入 text，立即生效并返回可分发的记录。
func (d *Document) Insert(pos int, text string) Record {
	runes := []rune(text)
	if pos < 0 || int32(pos) > visOf(d.root) {
		panic("collabdoc: insert position out of range")
	}
	parent := DocStart
	if pos > 0 {
		parent = kthVisible(d.root, int32(pos)-1).id
	}
	rec := Record{
		Author:  d.author,
		Seq:     d.nextRecSeq,
		Deps:    d.vv,
		Kind:    KindInsert,
		Parent:  parent,
		CharSeq: d.nextCharID,
		Text:    runes,
	}
	d.apply(rec)
	return rec
}

// Delete 删除可见正文中 [start, end) 码点区间，立即生效并返回可分发的记录。
func (d *Document) Delete(start, end int) Record {
	if start < 0 || end < start || int32(end) > visOf(d.root) {
		panic("collabdoc: delete range out of bounds")
	}
	targets := make([]CharID, 0, end-start)
	for i := int32(start); i < int32(end); i++ {
		targets = append(targets, kthVisible(d.root, i).id)
	}
	rec := Record{
		Author:  d.author,
		Seq:     d.nextRecSeq,
		Deps:    d.vv,
		Kind:    KindDelete,
		Targets: targets,
	}
	d.apply(rec)
	return rec
}

// Receive 接收一条记录；依赖未齐则暂缓，重复记录自动忽略。
// 返回时所有已收到且依赖齐全的记录均已生效。
func (d *Document) Receive(r Record) {
	if r.Author >= MaxAuthors {
		panic("collabdoc: record author out of range")
	}
	if r.Seq < d.vv[r.Author] {
		return // 已生效过的重复记录
	}
	key := recKey{r.Author, r.Seq}
	if _, ok := d.pendingIn[key]; ok {
		return // 已在等待队列中的重复记录
	}
	if d.depsMet(r) {
		d.apply(r)
	} else {
		d.pending = append(d.pending, &r)
		d.pendingIn[key] = struct{}{}
		return
	}
	d.drain()
}

// depsMet 报告记录的因果依赖是否已全部生效。
func (d *Document) depsMet(r Record) bool {
	if r.Seq != d.vv[r.Author] {
		return false
	}
	for i := 0; i < MaxAuthors; i++ {
		if r.Deps[i] > d.vv[i] {
			return false
		}
	}
	return true
}

// drain 反复扫描等待队列，生效所有依赖已齐的记录。
func (d *Document) drain() {
	for {
		progress := false
		rest := d.pending[:0]
		for _, p := range d.pending {
			if d.depsMet(*p) {
				delete(d.pendingIn, recKey{p.Author, p.Seq})
				d.apply(*p)
				progress = true
			} else {
				rest = append(rest, p)
			}
		}
		d.pending = rest
		if !progress {
			return
		}
	}
}

// apply 使一条依赖齐全的记录生效。调用方保证记录未生效过且依赖齐全。
func (d *Document) apply(r Record) {
	if r.Kind == KindInsert {
		d.applyInsert(r)
	} else {
		d.applyDelete(r)
	}
	d.vv[r.Author]++
	if r.Author == d.author {
		d.nextRecSeq++
		if r.Kind == KindInsert {
			d.nextCharID += uint32(len(r.Text))
		}
	}
}

func (d *Document) lookup(id CharID) *node {
	if id.Author == initialAuthor {
		if id.Seq < uint32(len(d.initial)) {
			return d.initial[id.Seq]
		}
		return nil
	}
	if id.Author < MaxAuthors && id.Seq < uint32(len(d.chars[id.Author])) {
		return d.chars[id.Author][id.Seq]
	}
	return nil
}

// insertIndex 计算新字符 (author, seq) 在 parent 之后的线性下标：
// 跳过 parent 的所有按排序规则应排在新字符之前的兄弟及其整棵子树。
func (d *Document) insertIndex(parent CharID, parentNode *node, author uint8, seq uint32) int32 {
	var idx int32
	if parentNode == nil {
		idx = 0
	} else {
		idx = indexOf(parentNode) + 1
	}
	total := sizeOf(d.root)
	for idx < total {
		s := nodeAt(d.root, idx)
		if s.parentID != parent || !sortsBefore(s, author, seq) {
			break
		}
		// 跳过 s 的整棵 RGA 子树：子树结束于第一个 depth <= s.depth 的节点。
		if end := firstDepthLE(d.root, 0, idx+1, s.depth); end >= 0 {
			idx = end
		} else {
			idx = total
		}
	}
	return idx
}

func (d *Document) applyInsert(r Record) {
	parent := r.Parent
	parentNode := d.lookup(parent) // DocStart 时 lookup 返回 nil
	var depth int32
	if parentNode != nil {
		depth = parentNode.depth + 1
	}
	seq := r.CharSeq
	for _, ch := range r.Text {
		id := CharID{Author: r.Author, Seq: seq}
		n := &node{ch: ch, id: id, parentID: parent, depth: depth, prio: d.rand()}
		idx := d.insertIndex(parent, parentNode, r.Author, seq)
		d.root = insertAt(d.root, n, idx)
		d.chars[r.Author] = append(d.chars[r.Author], n)
		parent = id
		parentNode = n
		depth++
		seq++
	}
}

func (d *Document) applyDelete(r Record) {
	for _, t := range r.Targets {
		if n := d.lookup(t); n != nil {
			tombstone(n)
		}
	}
}

// Read 返回当前可见正文。
func (d *Document) Read() string {
	out := make([]rune, 0, visOf(d.root))
	stack := make([]*node, 0, 64)
	n := d.root
	for n != nil || len(stack) > 0 {
		for n != nil {
			stack = append(stack, n)
			n = n.left
		}
		n = stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if !n.deleted {
			out = append(out, n.ch)
		}
		n = n.right
	}
	return string(out)
}

// Len 返回当前可见正文码点数。
func (d *Document) Len() int {
	return int(visOf(d.root))
}
