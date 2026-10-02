package docmerge

// MaxAuthors 是作者数量上限，作者编号为 0..MaxAuthors-1。
const MaxAuthors = 10

const (
	initAuthor     = -1 // 初始正文字符的作者编号
	sentinelAuthor = -2 // 文首哨兵的作者编号
	sentinelSeq    = -1 // 文首哨兵的字符序号
)

// Kind 是编辑记录的种类。
type Kind int

const (
	KindInsert Kind = iota
	KindDelete
)

// Range 表示一段连续字符：作者 Author 的字符流中 [Start, Start+Count)。
type Range struct {
	Author int
	Start  int
	Count  int
}

// Record 是一条不可变的编辑记录，可在副本之间转交。
// 调用方不得在转交后修改其内容。
type Record struct {
	Author  int
	Seq     int // 该作者的记录序号，从 0 递增
	Kind    Kind
	Version [MaxAuthors]int // 生成时已看见的各作者记录数（含自身本条）

	// 插入记录字段
	ParentAuthor int    // 左邻字符的作者；文首为 sentinelAuthor
	ParentSeq    int    // 左邻字符的序号
	CharBase     int    // 本段首字符在该作者字符流中的序号
	Text         []rune // 插入的文本

	// 删除记录字段
	Ranges []Range // 生成时实际看见并选中的字符
}

// Replica 是一个文档副本，正文与历史只存内存。不要求线程安全。
type Replica struct {
	author   int
	root     *node
	sentinel *node

	chars [MaxAuthors + 1][]*node // chars[author+1]，0 号槽为初始正文

	applied    [MaxAuthors]int // 各作者已生效记录数
	pending    []Record        // 等待依赖的记录
	pendingSet map[[2]int]bool

	rng    uint64
	walkID int
}

// NewReplica 以初始正文创建一个副本，author 取值 0..MaxAuthors-1。
// 所有副本必须用相同的 initialText 构造。
func NewReplica(initialText string, author int) *Replica {
	if author < 0 || author >= MaxAuthors {
		panic("docmerge: author out of range")
	}
	r := &Replica{
		author:     author,
		pendingSet: make(map[[2]int]bool),
		rng:        0x9e3779b97f4a7c15 ^ uint64(author+1),
	}
	r.sentinel = &node{
		author:  sentinelAuthor,
		seq:     sentinelSeq,
		deleted: true, // 哨兵不可见
		prio:    r.nextPrio(),
		size:    1,
	}
	r.root = r.sentinel
	prev := r.sentinel
	for _, ch := range initialText {
		nd := &node{
			ch:         ch,
			author:     initAuthor,
			seq:        len(r.chars[0]),
			crdtParent: prev,
			prio:       r.nextPrio(),
		}
		r.chars[0] = append(r.chars[0], nd)
		r.insertAfter(prev, nd)
		prev = nd
	}
	return r
}

// nextPrio 用 splitmix64 生成 treap 优先级。
func (r *Replica) nextPrio() uint32 {
	r.rng += 0x9e3779b97f4a7c15
	z := r.rng
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	z = z ^ (z >> 31)
	return uint32(z >> 32)
}

// lookup 按字符 ID 取节点；哨兵 ID 返回哨兵。
func (r *Replica) lookup(author, seq int) *node {
	if author == sentinelAuthor {
		return r.sentinel
	}
	slot := author + 1
	if slot < 0 || slot >= len(r.chars) {
		return nil
	}
	s := r.chars[slot]
	if seq < 0 || seq >= len(s) {
		return nil
	}
	return s[seq]
}

// insertAfter 把 n 插入到 treap 中 pos 的紧后方。
func (r *Replica) insertAfter(pos, n *node) {
	if pos.right == nil {
		pos.right = n
		n.parent = pos
	} else {
		m := pos.right
		for m.left != nil {
			m = m.left
		}
		m.left = n
		n.parent = m
	}
	update(n)
	rotateUp(n)
	fixUp(n)
	if n.parent == nil {
		r.root = n
	}
}

// commit 让本地产生的记录立即生效并推进自身版本。
func (r *Replica) commit(rec Record) Record {
	r.apply(rec)
	r.applied[rec.Author]++
	return rec
}

// newRecord 构造本地记录的公共头部。
func (r *Replica) newRecord(kind Kind) Record {
	rec := Record{Author: r.author, Seq: r.applied[r.author], Kind: kind}
	rec.Version = r.applied
	rec.Version[r.author]++
	return rec
}

// Insert 在可见位置 pos（码点计，0..Len()）插入文本，返回可转交的记录。
func (r *Replica) Insert(pos int, text string) Record {
	if pos < 0 || pos > r.Len() {
		panic("docmerge: insert position out of range")
	}
	runes := []rune(text)
	rec := r.newRecord(KindInsert)
	parent := r.sentinel
	if pos > 0 {
		parent = findByVisible(r.root, pos-1)
	}
	rec.ParentAuthor = parent.author
	rec.ParentSeq = parent.seq
	rec.CharBase = len(r.chars[r.author+1])
	rec.Text = runes
	return r.commit(rec)
}

// Delete 删除可见区间 [start, end)（码点计），返回可转交的记录。
func (r *Replica) Delete(start, end int) Record {
	if start < 0 || end < start || end > r.Len() {
		panic("docmerge: delete range out of range")
	}
	rec := r.newRecord(KindDelete)
	if start < end {
		n := findByVisible(r.root, start)
		for remain := end - start; remain > 0; remain-- {
			rec.Ranges = appendRange(rec.Ranges, n.author, n.seq)
			for n = succ(n); n != nil && n.deleted; n = succ(n) {
			}
		}
	}
	return r.commit(rec)
}

// appendRange 把一个字符 ID 并入区间列表，相邻则扩展末段。
func appendRange(ranges []Range, author, seq int) []Range {
	if n := len(ranges); n > 0 {
		last := &ranges[n-1]
		if last.Author == author && last.Start+last.Count == seq {
			last.Count++
			return ranges
		}
	}
	return append(ranges, Range{Author: author, Start: seq, Count: 1})
}

// Text 返回当前正文。
func (r *Replica) Text() string {
	out := make([]rune, 0, r.Len())
	// 迭代中序遍历，跳过墓碑。
	stack := make([]*node, 0, 64)
	for n := r.root; n != nil || len(stack) > 0; {
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

// Len 返回当前正文的码点长度。
func (r *Replica) Len() int {
	return visOf(r.root)
}
