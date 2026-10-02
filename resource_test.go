package collabdoc

import (
	"math/rand"
	"runtime"
	"strings"
	"testing"
	"time"
)

// 规模上限（来自需求）：
const (
	resInitialLen   = 500000 // 初始正文码点
	resInsertRecs   = 5000   // 插入记录数
	resInsertRunLen = 100    // 每条插入记录的文本长度 → 累计 50 万码点
	resDeleteRecs   = 2000   // 删除记录数
	resDeleteLen    = 1000   // 每条删除记录选中长度 → 累计 200 万码点
	resDeliveries   = 3      // 每条记录最多投递次数
	resMaxMillis    = 30000  // 接收全部记录并读出一次最终正文的时限
	resMaxMemBytes  = 512 << 20
)

// TestResourceScale 在累计规模上限附近验收：
// 初始 50 万码点 + 50 万码点插入（共 100 万）+ 累计 200 万码点删除选中，
// 7000 条唯一记录、每条最多投递 3 次。
// 生成阶段逐个创建作者副本并及时释放；验收阶段进程只驻留一个副本。
func TestResourceScale(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping resource test in -short mode")
	}
	r := rand.New(rand.NewSource(1))

	// ---- 生成阶段：10 名作者各自在独立副本上产生记录（串行生成，及时释放）。
	initial := strings.Repeat("初始文本ab𠀀", resInitialLen/7) + "甲乙丙丁" // 7 码点一组，补齐到 50 万
	if len([]rune(initial)) != resInitialLen {
		t.Fatalf("initial length = %d", len([]rune(initial)))
	}
	var records []Record
	for a := 0; a < MaxAuthors; a++ {
		gen := New(uint8(a), initial)
		// 插入：每人 500 条 × 100 码点。
		for i := 0; i < resInsertRecs/MaxAuthors; i++ {
			pos := r.Intn(gen.Len() + 1)
			records = append(records, gen.Insert(pos, strings.Repeat("甲乙丙丁戊己庚辛壬癸", resInsertRunLen/10)))
		}
		// 删除：每人 200 条 × 1000 码点（并发区间可重叠，累计计入上限）。
		for i := 0; i < resDeleteRecs/MaxAuthors; i++ {
			n := gen.Len()
			if n < resDeleteLen {
				continue
			}
			start := r.Intn(n - resDeleteLen + 1)
			records = append(records, gen.Delete(start, start+resDeleteLen))
		}
		gen = nil
	}
	runtime.GC()
	if len(records) > 10000 {
		t.Fatalf("too many records: %d", len(records))
	}
	t.Logf("unique records: %d", len(records))

	// ---- 验收阶段：进程只驻留一个副本。
	var memBefore, memAfter runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&memBefore)

	doc := New(0, initial)
	start := time.Now()

	// 每条记录投递 resDeliveries 次，整体随机乱序（含因果缺口与重复）。
	var deliveries []Record
	for _, rec := range records {
		for k := 0; k < resDeliveries; k++ {
			deliveries = append(deliveries, rec)
		}
	}
	r.Shuffle(len(deliveries), func(i, j int) { deliveries[i], deliveries[j] = deliveries[j], deliveries[i] })
	for _, rec := range deliveries {
		doc.Receive(rec)
	}
	final := doc.Read()
	elapsed := time.Since(start)

	runtime.GC()
	runtime.ReadMemStats(&memAfter)
	runtime.KeepAlive(doc) // 保证统计时副本仍然驻留

	t.Logf("receive+read elapsed: %v (limit %d ms)", elapsed, resMaxMillis)
	t.Logf("final visible length: %d codepoints", len([]rune(final)))
	t.Logf("heap alloc after load: %d MiB (limit 512 MiB)", memAfter.HeapAlloc>>20)
	t.Logf("total alloc during load: %d MiB", (memAfter.TotalAlloc-memBefore.TotalAlloc)>>20)

	if elapsed > resMaxMillis*time.Millisecond {
		t.Fatalf("too slow: %v", elapsed)
	}
	if memAfter.HeapAlloc > resMaxMemBytes {
		t.Fatalf("heap alloc %d exceeds 512 MiB", memAfter.HeapAlloc)
	}

	// 正确性抽查：把同一批记录以不同顺序喂给第二个副本，结果必须一致。
	doc2 := New(1, initial)
	perm := r.Perm(len(records))
	for _, i := range perm {
		doc2.Receive(records[i])
		doc2.Receive(records[i])
	}
	if doc2.Read() != final {
		t.Fatal("replicas diverge at scale")
	}
}
