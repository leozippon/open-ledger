package server

import (
	"strings"
	"testing"
	"time"

	"ledger/internal/store"
)

func TestRecognizePromptDocument(t *testing.T) {
	got := systemPrompt(
		[]store.Category{
			{ID: 1, Kind: store.KindExpense, Name: "餐饮"},
			{ID: 2, Kind: store.KindExpense, Name: "交通"},
			{ID: 3, Kind: store.KindExpense, Name: "购物"},
			{ID: 4, Kind: store.KindExpense, Name: "居住"},
			{ID: 5, Kind: store.KindExpense, Name: "娱乐"},
			{ID: 6, Kind: store.KindExpense, Name: "医疗"},
			{ID: 7, Kind: store.KindExpense, Name: "教育"},
			{ID: 8, Kind: store.KindExpense, Name: "通讯"},
			{ID: 9, Kind: store.KindExpense, Name: "人情"},
			{ID: 10, Kind: store.KindExpense, Name: "其他"},
			{ID: 11, Kind: store.KindIncome, Name: "工资"},
			{ID: 12, Kind: store.KindIncome, Name: "奖金"},
			{ID: 13, Kind: store.KindIncome, Name: "理财"},
			{ID: 14, Kind: store.KindIncome, Name: "兼职"},
			{ID: 15, Kind: store.KindIncome, Name: "其他"},
			{ID: 99, Kind: store.KindExpense, Name: "已归档", Archived: true},
		},
		[]store.Activity{
			{ID: 1, Name: "日常生活", IsDefault: true},
			{ID: 2, Name: "香港旅行", StartDate: "2026-09-01"},
		},
		[]store.Card{
			{ID: 1, Kind: store.CardDebit, Bank: "招商银行", Name: "日常", Last4: "1234"},
			{ID: 2, Kind: store.CardCredit, Bank: "中信银行", Last4: "8888"},
			{ID: 9, Kind: store.CardDebit, Bank: "旧卡", Last4: "0000", Archived: true},
		},
		[]store.Transaction{
			{Date: "2026-09-21", Kind: store.KindExpense, Amount: 1361, CategoryName: "交通", ActivityName: "日常生活", CardBank: "招商银行", CardName: "日常", CardLast4: "1234", Note: "打车"},
			{Date: "2026-09-20", Kind: store.KindExpense, Amount: 2600, CategoryName: "餐饮", ActivityName: "日常生活", Note: "喜茶", Shared: true},
			{Date: "2026-09-01", Kind: store.KindIncome, Amount: 2000000, CategoryName: "工资", ActivityName: "日常生活"},
		},
	)
	want := strings.TrimPrefix(`
根据文字或图片整理家庭账本。

一条对应一笔独立订单或一次独立付款。同一付款里的多件商品不要拆开；不同订单或不同付款不要合并。
结售汇或跨境汇款拆成相邻两笔：先在汇出卡上兑换，再把买入的货币转到收款卡。手续费为零则不另记。

只输出一个 JSON 对象，每个键名都加双引号。例如 {"entries":[{"kind":"exchange","amount":100.00,"currency":"CNY","to_amount":110.00,"to_currency":"HKD","card_name":"汇出卡","date":"2026-09-23","note":"购汇","shared":false}]}。
编号只能从下面的列表原样抄，不要沿用例子里的数字。
kind 为 expense、income、exchange 或 transfer。金额写数字且必须大于 0，不要千分位逗号；货币用 CNY、HKD、USD 这类代码。
支出和收入：amount 为人民币元。category_id、activity_id 必须是下列编号，每笔单独选；活动看不出则选默认。card_id 能对应则填，看不出则 0。
兑换：amount 与 currency 是卖出，to_amount 与 to_currency 是买入，发生在 card_id 这一张卡上。
转账：amount 与 currency 从 card_id 转到 to_card_id，两张卡都要从下列银行卡里对应上。
date 为 YYYY-MM-DD。note 简短，只写这一笔；没有则空字符串。
shared 在全家一起时为 true，个人、兑换、转账或看不出时为 false。

分类
1 支出 餐饮
2 支出 交通
3 支出 购物
4 支出 居住
5 支出 娱乐
6 支出 医疗
7 支出 教育
8 支出 通讯
9 支出 人情
10 支出 其他
11 收入 工资
12 收入 奖金
13 收入 理财
14 收入 兼职
15 收入 其他

活动
1 日常生活 默认
2 香港旅行 2026-09-01

银行卡
1 储蓄 招商银行 日常 1234
2 信用 中信银行 8888

近期
2026-09-21 支出 13.61 交通 日常生活 招商银行 日常 1234 打车 个人
2026-09-20 支出 26.00 餐饮 日常生活 喜茶 共同
2026-09-01 收入 20000.00 工资 日常生活 个人
`, "\n")
	if got != want {
		t.Fatalf("system prompt =\n%s\nwant\n%s", got, want)
	}
	if user := buildUserText("", time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)); user != "今天是 2026-09-21。" {
		t.Fatalf("image user text = %q", user)
	}
	if user := buildUserText("喜茶 26 元", time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)); user != "今天是 2026-09-21。\n喜茶 26 元" {
		t.Fatalf("sms user text = %q", user)
	}
}

func TestParseNullCardAndNote(t *testing.T) {
	drafts, err := parseModelJSON(`{"entries":[{"kind":"exchange","amount":1342.05,"currency":"CNY","to_amount":1565.79,"to_currency":"HKD","card_id":null,"date":"2026-09-24","note":"汇出卡6217****4102"},{"kind":"transfer","amount":1565.79,"currency":"HKD","card_id":null,"to_card_id":null,"card_last4":"4102","date":"2026-09-24","note":"汇入 ZA Bank Limited"}]}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cards := []store.Card{
		{ID: 1, Kind: store.CardDebit, Bank: "工商银行", Last4: "4102"},
		{ID: 4, Kind: store.CardDebit, Bank: "众安银行", Last4: "0817"},
	}
	out, err := bindDrafts(drafts, nil, nil, cards)
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if out.Entries[0].CardID != 1 || out.Entries[1].CardID != 1 || out.Entries[1].ToCardID != 4 {
		t.Fatalf("entries = %+v", out.Entries)
	}
}

func TestParseLooseRemittanceJSON(t *testing.T) {
	drafts, err := parseModelJSON(`{"entries":[{kind:"exchange",amount:"1,342.05",currency:"CNY",to_amount:"1,565.79",to_currency:"港币",card_last4:"4102"},{kind:"transfer",amount:1565.79,currency:"HKD",card_last4:"4102",to_card_name:"ZA Bank Limited"}]}`)
	if err != nil {
		t.Fatalf("parse loose json: %v", err)
	}
	if len(drafts) != 2 || drafts[0].Kind != "exchange" || drafts[1].Kind != "transfer" {
		t.Fatalf("drafts = %+v", drafts)
	}
	cards := []store.Card{
		{ID: 1, Kind: store.CardDebit, Bank: "工商银行", Last4: "4102"},
		{ID: 4, Kind: store.CardDebit, Bank: "众安银行", Name: "最股励", Last4: "0817"},
	}
	out, err := bindDrafts(drafts, nil, nil, cards)
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if len(out.Entries) != 2 {
		t.Fatalf("entries = %+v", out.Entries)
	}
	if out.Entries[0].Amount != 134205 || out.Entries[0].ToAmount != 156579 || out.Entries[0].Currency != "CNY" || out.Entries[0].ToCurrency != "HKD" || out.Entries[0].CardID != 1 {
		t.Fatalf("exchange = %+v", out.Entries[0])
	}
	if out.Entries[1].Kind != store.KindTransfer || out.Entries[1].Amount != 156579 || out.Entries[1].CardID != 1 || out.Entries[1].ToCardID != 4 {
		t.Fatalf("transfer = %+v", out.Entries[1])
	}
}
