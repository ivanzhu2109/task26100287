package docmerge

import (
	"math/rand"
	"testing"
)

// 验收用例 3：10 名作者、多轮分支编辑、含中文/补充平面/组合字符；
// 在已生效记录集合相同的观察点，各副本正文必须逐码点一致。
func TestTenAuthorsConvergence(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	initial := "标题：协作 é 合并🎉\n正文第一段，含组合字符 e\u0301 与补充平面 𝄞。\n第二段開始。\n"

	reps := make([]*Replica, MaxAuthors)
	for a := 0; a < MaxAuthors; a++ {
		reps[a] = NewReplica(initial, a)
	}

	pieces := []string{
		"甲", "乙丙", "hello", "😀🎉", "𝄞𝄢", "e\u0301", "a\u0308o\u0308",
		"新篇章，", "【批注】", "。", "\n分段\n", "xyz", "界", "🧑‍💻",
	}

	// 多轮分支编辑：各作者只在自己副本上编辑，产生记录。
	var records []Record
	for round := 0; round < 40; round++ {
		for a := 0; a < MaxAuthors; a++ {
			r := reps[a]
			ops := rng.Intn(3)
			for i := 0; i < ops; i++ {
				if r.Len() == 0 || rng.Intn(10) < 6 {
					pos := rng.Intn(r.Len() + 1)
					text := pieces[rng.Intn(len(pieces))]
					if rng.Intn(3) == 0 {
						text += pieces[rng.Intn(len(pieces))]
					}
					records = append(records, r.Insert(pos, text))
				} else {
					start := rng.Intn(r.Len())
					end := start + 1 + rng.Intn(8)
					if end > r.Len() {
						end = r.Len()
					}
					records = append(records, r.Delete(start, end))
				}
			}
		}
	}
	if len(records) == 0 {
		t.Fatal("没有产生任何记录")
	}

	// 投递列表：每条记录重复 1..3 次，整体随机乱序（含因果缺口）。
	type delivery struct {
		rec Record
	}
	var queue []delivery
	for _, rec := range records {
		for k := 0; k < 1+rng.Intn(3); k++ {
			queue = append(queue, delivery{rec})
		}
	}
	rng.Shuffle(len(queue), func(i, j int) { queue[i], queue[j] = queue[j], queue[i] })

	// 观察点：已生效记录集合（各作者生效数向量）相同 => 正文必须一致。
	type obsKey [MaxAuthors]int
	observed := make(map[obsKey]string)
	observe := func(r *Replica) {
		var k obsKey
		copy(k[:], r.applied[:])
		txt := r.Text()
		if prev, ok := observed[k]; ok {
			if prev != txt {
				t.Fatalf("相同已生效集合得到不同正文:\n%q\nvs\n%q", prev, txt)
			}
		} else {
			observed[k] = txt
		}
	}

	for _, d := range queue {
		for _, r := range reps {
			before := r.applied
			r.Receive(d.rec)
			if r.applied != before {
				observe(r)
			}
		}
	}

	// 全部记录投递完毕后，所有副本必须收敛到同一正文。
	want := reps[0].Text()
	for a := 1; a < MaxAuthors; a++ {
		if got := reps[a].Text(); got != want {
			t.Fatalf("副本 %d 未收敛:\n%q\nvs\n%q", a, got, want)
		}
	}

	// 再整体重复投递一遍：不得产生任何新效果。
	for _, d := range queue {
		for _, r := range reps {
			r.Receive(d.rec)
		}
	}
	for a := 0; a < MaxAuthors; a++ {
		if got := reps[a].Text(); got != want {
			t.Fatalf("重复投递后副本 %d 正文改变", a)
		}
	}
	t.Logf("唯一记录 %d 条，投递 %d 次，最终正文 %d 码点，观察点 %d 个",
		len(records), len(queue), len([]rune(want)), len(observed))
}
