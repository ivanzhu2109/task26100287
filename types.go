package collabdoc

// MaxAuthors 是支持的作者数量，作者编号为 0..MaxAuthors-1。
const MaxAuthors = 10

// initialAuthor 是初始正文字符使用的保留作者编号。
const initialAuthor uint8 = 0xFF

// CharID 全局唯一标识一个字符。
// Author 为 0xFF 时表示初始正文字符，Seq 为其在初始正文中的下标。
type CharID struct {
	Author uint8
	Seq    uint32
}

// DocStart 是特殊的父引用，表示"文首"（虚拟根之前没有具体字符）。
var DocStart = CharID{Author: initialAuthor, Seq: ^uint32(0)}

// RecordKind 区分编辑记录类型。
type RecordKind uint8

const (
	KindInsert RecordKind = iota
	KindDelete
)

// Record 是一条不可变的编辑记录，可在副本之间任意转交、乱序、重复投递。
type Record struct {
	Author  uint8              // 产生该记录的作者编号
	Seq     uint32             // 该作者的记录序号，从 0 开始连续递增
	Deps    [MaxAuthors]uint32 // 生成记录时已生效的各作者记录数（版本向量）
	Kind    RecordKind
	Parent  CharID   // 仅插入：首字符插入到哪个字符之后；DocStart 表示文首
	CharSeq uint32   // 仅插入：本段文本首字符的字符序号
	Text    []rune   // 仅插入：插入的文本（按码点）
	Targets []CharID // 仅删除：生成时实际看见并选中的字符
}
