package collabdoc

import (
	"math/rand"
	"testing"
)

// 含中文、补充平面字符与组合字符的随机文本片段。
var fragRunes = [][]rune{
	[]rune("你好"),
	[]rune("世界"),
	[]rune("ab"),
	[]rune("XYZ"),
	[]rune("𠀀𠀁"), // 补充平面
	[]rune("😀🎉"), // emoji（补充平面）
	[]rune("é"), // e + 组合重音符
	[]rune("가"),  // 谚文
	[]rune("!?,。"),
}

func randFrag(r *rand.Rand) []rune {
	f := fragRunes[r.Intn(len(fragRunes))]
	out := make([]rune, len(f))
	copy(out, f)
	return out
}

// TestTenAuthorsConvergence 模拟 10 名作者多轮分支编辑：
// 每轮各作者在本地随机插入/删除产生记录，随后把迄今全部记录
// 以随机顺序、最多 3 次重复投递到每个副本。
// 每轮结束后所有副本已生效记录集合相同，正文必须逐码点一致；
// 轮次中途凡版本向量一致的副本对，正文也必须一致。
func TestTenAuthorsConvergence(t *testing.T) {
	r := rand.New(rand.NewSource(20261002))
	const authors = 10
	initial := "初始正文ab𠀀cd你好"
	docs := make([]*Document, authors)
	for i := range docs {
		docs[i] = New(uint8(i), initial)
	}

	var history []Record
	checkpoint := func(round int) {
		// 把全部历史记录以独立随机顺序、随机重复投递到每个副本。
		for _, d := range docs {
			perm := r.Perm(len(history))
			for _, i := range perm {
				d.Receive(history[i])
				if r.Intn(3) == 0 {
					d.Receive(history[i]) // 重复投递
				}
			}
		}
		want := docs[0].Read()
		for i := 1; i < authors; i++ {
			if got := docs[i].Read(); got != want {
				t.Fatalf("round %d: replica %d diverges:\n%q\nvs\n%q", round, i, got, want)
			}
		}
	}

	for round := 0; round < 6; round++ {
		for a := 0; a < authors; a++ {
			d := docs[a]
			ops := 1 + r.Intn(3)
			for op := 0; op < ops; op++ {
				n := d.Len()
				if n > 0 && r.Intn(3) == 0 {
					start := r.Intn(n)
					end := start + 1 + r.Intn(min(8, n-start))
					history = append(history, d.Delete(start, end))
				} else {
					pos := r.Intn(n + 1)
					history = append(history, d.Insert(pos, string(randFrag(r))))
				}
			}
		}
		checkpoint(round)
	}

	// 最终全文再交换一轮（含重复），结果仍一致。
	checkpoint(99)
	t.Logf("final length = %d codepoints, records = %d", docs[0].Len(), len(history))
}

// TestSameAppliedSetSameText 验证：只要两个副本已生效记录集合相同，
// 无论到达顺序与重复次数如何，读取结果逐码点一致。
func TestSameAppliedSetSameText(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	const authors = 4
	src := make([]*Document, authors)
	for i := range src {
		src[i] = New(uint8(i), "abc甲乙𠀀")
	}
	var records []Record
	for i := 0; i < 60; i++ {
		d := src[r.Intn(authors)]
		n := d.Len()
		if n > 2 && r.Intn(2) == 0 {
			start := r.Intn(n - 1)
			end := start + 1 + r.Intn(min(5, n-start))
			records = append(records, d.Delete(start, end))
		} else {
			records = append(records, d.Insert(r.Intn(n+1), string(randFrag(r))))
		}
		// 源副本之间即时同步，制造分支历史。
		for _, other := range src {
			if other != d {
				other.Receive(records[len(records)-1])
			}
		}
	}

	// 取同一批记录的不同随机子集（保持因果闭合：反复随机剔除依赖缺失者），
	// 以不同顺序与重复次数喂给两个全新副本，结果必须一致。
	for trial := 0; trial < 20; trial++ {
		subset := make([]Record, 0, len(records))
		for _, rec := range records {
			if r.Intn(2) == 0 {
				subset = append(subset, rec)
			}
		}
		subset = causalClosure(subset)
		a, b := New(9, "abc甲乙𠀀"), New(9, "abc甲乙𠀀")
		feed := func(d *Document) {
			perm := r.Perm(len(subset))
			for _, i := range perm {
				times := 1 + r.Intn(3)
				for k := 0; k < times; k++ {
					d.Receive(subset[i])
				}
			}
		}
		feed(a)
		feed(b)
		if a.Read() != b.Read() {
			t.Fatalf("trial %d: %q vs %q", trial, a.Read(), b.Read())
		}
	}
}

// causalClosure 反复移除依赖不在集合内的记录，直到集合因果闭合。
func causalClosure(recs []Record) []Record {
	for {
		applied := [MaxAuthors]uint32{}
		kept := recs[:0]
		changed := false
		// 多趟扫描，模拟 drain 语义。
		rest := append([]Record(nil), recs...)
		for {
			progress := false
			next := rest[:0]
			for _, rec := range rest {
				ok := rec.Seq == applied[rec.Author]
				for i := 0; i < MaxAuthors && ok; i++ {
					if rec.Deps[i] > applied[i] {
						ok = false
					}
				}
				if ok {
					kept = append(kept, rec)
					applied[rec.Author]++
					progress = true
				} else {
					next = append(next, rec)
				}
			}
			rest = next
			if !progress {
				break
			}
		}
		if len(rest) == 0 {
			return kept
		}
		changed = true
		recs = kept
		if !changed {
			return kept
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
