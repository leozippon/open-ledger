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

只输出 {"entries":[{kind,amount,category_id,category_name,activity_id,activity_name,card_id,card_name,date,note,shared}]}。
kind 为 expense 或 income。amount 为人民币元，最多两位小数。
category_id、activity_id 必须是下列编号，每笔单独选最合适的一个；活动看不出则选默认。
card_id 能对应到卡则填编号，看不出则 0。
date 为 YYYY-MM-DD。note 简短，只写这一笔，并模仿近期备注；没有则空字符串。
shared 在全家一起时为 true，个人或看不出时为 false。

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
