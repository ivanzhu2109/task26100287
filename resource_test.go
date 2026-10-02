package docmerge

import (
	"math/rand"
	"runtime"
	"testing"
	"time"
)

// 资源验收：达到累计规模上限（初始 50 万码点、1 万条唯一编辑、
// 插入累计 100 万码点、删除区间累计 200 万码点、每记录投递 3 次），
// 进程只驻留一个副本；接收全部记录并读出一次最终正文应在 30 秒内，
// 堆内存应远低于 512 MiB。
func TestResourceLimits(t *testing.T) {
	const (
		initLen    = 500000
		localIns   = 1000 // 本地插入记录数
		localDel   = 1000 // 本地删除记录数
		remoteRec  = 7992 // 远端记录数（作者 1..9）
		insRunes   = 100  // 每条插入记录的码点数
		delRunes   = 400  // 每条删除记录选中的码点数
		deliveries = 3    // 每条记录投递次数
	)
	rng := rand.New(rand.NewSource(87))

	// 初始正文：混合 ASCII、中文、补充平面字符，共 50 万码点。
	base := []rune("文档正文document🎉含组合e\u0301字符。")
	initRunes := make([]rune, 0, initLen)
	for len(initRunes) < initLen {
		initRunes = append(initRunes, base...)
	}
	initRunes = initRunes[:initLen]

	r := NewReplica(string(initRunes), 0)

	// 预生成远端记录：作者 1..9 各自的插入与删除交替，按记录序号排列。
	remote := make([]Record, 0, remoteRec)
	perAuthor := remoteRec / (MaxAuthors - 1) // 每人 888 条
	for a := 1; a < MaxAuthors; a++ {
		seq, charBase := 0, 0
		for i := 0; i < perAuthor; i++ {
			var rec Record
			if i%2 == 0 {
				text := make([]rune, insRunes)
				for j := range text {
					text[j] = base[rng.Intn(len(base))]
				}
				rec = Record{
					Author: a, Seq: seq, Kind: KindInsert,
					ParentAuthor: initAuthor,
					ParentSeq:    rng.Intn(initLen),
					CharBase:     charBase,
					Text:         text,
				}
				charBase += insRunes
			} else {
				rec = Record{
					Author: a, Seq: seq, Kind: KindDelete,
					Ranges: []Range{{Author: initAuthor, Start: rng.Intn(initLen - delRunes), Count: delRunes}},
				}
			}
			rec.Version[a] = seq + 1
			seq++
			remote = append(remote, rec)
		}
	}

	start := time.Now()

	// 本地编辑与远端记录交错进行；远端记录按作者轮询、批内乱序、每条投 3 次。
	next := make([]int, MaxAuthors) // 各远端作者下一条待投记录在 remote 中的下标
	idxOf := func(a, k int) int { return (a-1)*perAuthor + k }
	localText := []rune("本地插入的文本🎉local-insert-text。")
	for len(localText) < insRunes {
		localText = append(localText, localText...)
	}
	localText = localText[:insRunes]
	rounds := localIns // 1000 轮，每轮 1 条本地插入 + 1 条本地删除
	for round := 0; round < rounds; round++ {
		pos := 0
		if r.Len() > 0 {
			pos = rng.Intn(r.Len() + 1)
		}
		r.Insert(pos, string(localText))
		if r.Len() > delRunes {
			st := rng.Intn(r.Len() - delRunes)
			r.Delete(st, st+delRunes)
		}
		// 每轮投递约 8 条远端记录（各自重复 3 次）。
		var batch []Record
		for k := 0; k < remoteRec/rounds; k++ {
			a := 1 + (round*8+k)%(MaxAuthors-1)
			if next[a] < perAuthor {
				batch = append(batch, remote[idxOf(a, next[a])])
				next[a]++
			}
		}
		rng.Shuffle(len(batch), func(i, j int) { batch[i], batch[j] = batch[j], batch[i] })
		for d := 0; d < deliveries; d++ {
			for _, rec := range batch {
				r.Receive(rec)
			}
		}
	}
	// 兜底：投完剩余远端记录。
	for a := 1; a < MaxAuthors; a++ {
		for ; next[a] < perAuthor; next[a]++ {
			for d := 0; d < deliveries; d++ {
				r.Receive(remote[idxOf(a, next[a])])
			}
		}
	}

	final := r.Text()
	elapsed := time.Since(start)

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	t.Logf("唯一记录 %d 条，最终正文 %d 码点，耗时 %v", localIns+localDel+remoteRec, len([]rune(final)), elapsed)
	t.Logf("堆内存 Alloc=%.1f MiB, Sys=%.1f MiB", float64(ms.Alloc)/(1<<20), float64(ms.Sys)/(1<<20))

	if elapsed > 30*time.Second {
		t.Fatalf("接收并读出耗时 %v，超过 30 秒", elapsed)
	}
	if ms.Sys > 512<<20 {
		t.Fatalf("内存 Sys=%d 字节，超过 512 MiB", ms.Sys)
	}
	if len([]rune(final)) == 0 {
		t.Fatal("最终正文为空")
	}
}
