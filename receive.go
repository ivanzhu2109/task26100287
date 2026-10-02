package docmerge

// Receive 接收一条编辑记录。记录可乱序、重复到达；重复接收无效。
// 依赖未齐的记录进入缓冲，依赖补齐后自动生效；
// 返回时所有已收到且依赖齐全的记录都已生效。
func (r *Replica) Receive(rec Record) {
	if rec.Author < 0 || rec.Author >= MaxAuthors || rec.Seq < 0 {
		return
	}
	if rec.Seq < r.applied[rec.Author] {
		return // 已生效过的重复记录
	}
	key := [2]int{rec.Author, rec.Seq}
	if r.pendingSet[key] {
		return // 已在缓冲中的重复记录
	}
	r.pendingSet[key] = true
	r.pending = append(r.pending, rec)
	r.flush()
}

// flush 反复扫描缓冲，让所有依赖齐全的记录生效。
func (r *Replica) flush() {
	for {
		progress := false
		for i := 0; i < len(r.pending); {
			rec := r.pending[i]
			if !r.applicable(rec) {
				i++
				continue
			}
			r.pending = append(r.pending[:i], r.pending[i+1:]...)
			delete(r.pendingSet, [2]int{rec.Author, rec.Seq})
			r.apply(rec)
			r.applied[rec.Author]++
			progress = true
		}
		if !progress {
			return
		}
	}
}

// applicable 判断记录当前是否满足生效条件：
// 该作者的前序记录已生效，且生成时看见的所有记录都已生效。
func (r *Replica) applicable(rec Record) bool {
	if rec.Seq != r.applied[rec.Author] {
		return false
	}
	for a := 0; a < MaxAuthors; a++ {
		need := rec.Version[a]
		if a == rec.Author {
			need-- // Version 含本条自身，生效前不计入
		}
		if r.applied[a] < need {
			return false
		}
	}
	return true
}

// apply 让一条记录生效（调用前须确认 applicable）。
func (r *Replica) apply(rec Record) {
	switch rec.Kind {
	case KindInsert:
		r.applyInsert(rec)
	case KindDelete:
		r.applyDelete(rec)
	}
}

// applyInsert 让一条插入记录生效。
// 同一父字符下的兄弟块排序键为：非初始正文块先于初始正文字符，
// 非初始块之间按（作者升序，同作者字符序号降序）。
// 因此同一缝隙的并发插入按作者编号升序排列，整段不互相穿插；
// 初始正文链条中的后继字符总是排在新插入块之后，保证
// “在字符 b 前插入”的本地语义被保留。
func (r *Replica) applyInsert(rec Record) {
	if len(rec.Text) == 0 {
		return
	}
	parent := r.lookup(rec.ParentAuthor, rec.ParentSeq)
	if parent == nil {
		return // 依赖保证不会发生，防御性忽略
	}
	// 兄弟遍历：从 parent 的中序后继开始，凡 crdtParent 在本次
	// 遍历中已见过的节点都属于 parent 的子树；遇到子树外节点即停。
	r.walkID++
	parent.walk = r.walkID
	prev := parent
	for n := succ(parent); n != nil; n = succ(n) {
		if n.crdtParent == nil || n.crdtParent.walk != r.walkID {
			break // 已越过 parent 的子树末端
		}
		n.walk = r.walkID
		if n.crdtParent == parent {
			// n 是新块的兄弟：若 n 排在新块之后则停。
			if n.author == initAuthor ||
				n.author > rec.Author ||
				(n.author == rec.Author && n.seq < rec.CharBase) {
				break
			}
		}
		prev = n
	}
	slot := rec.Author + 1
	for need := rec.CharBase + len(rec.Text); len(r.chars[slot]) < need; {
		r.chars[slot] = append(r.chars[slot], nil)
	}
	crdtParent := parent
	for i, ch := range rec.Text {
		nd := &node{
			ch:         ch,
			author:     rec.Author,
			seq:        rec.CharBase + i,
			crdtParent: crdtParent,
			prio:       r.nextPrio(),
		}
		r.chars[slot][nd.seq] = nd
		r.insertAfter(prev, nd)
		prev = nd
		crdtParent = nd
	}
}

// applyDelete 让一条删除记录生效：只标记记录中显式选中的字符，
// 并发插入在区间内部或边界的字符不受影响；重复删除是幂等的。
func (r *Replica) applyDelete(rec Record) {
	for _, rg := range rec.Ranges {
		slot := rg.Author + 1
		if slot < 0 || slot >= len(r.chars) {
			continue
		}
		s := r.chars[slot]
		for i := 0; i < rg.Count; i++ {
			idx := rg.Start + i
			if idx < 0 || idx >= len(s) {
				continue
			}
			nd := s[idx]
			if nd == nil || nd.deleted {
				continue
			}
			nd.deleted = true
			fixUp(nd)
		}
	}
}
