// Command demo writes a two-member household with a few months of sample bills.
package main

import (
	"fmt"
	"log"
	"os"

	"ledger/internal/store"
)

const (
	adminName = "小陈"
	adminPass = "demodemo"
	memberName = "小周"
	memberPass = "demodemo"
)

func main() {
	path := os.Getenv("LEDGER_DB")
	if path == "" {
		path = "demo.db"
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		log.Fatal(err)
	}
	for _, extra := range []string{path + "-wal", path + "-shm"} {
		_ = os.Remove(extra)
	}

	st, err := store.Open(path, store.Admin{Username: adminName, Password: adminPass})
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()

	if err := seed(st); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("demo ledger written to %s\n", path)
	fmt.Printf("sign in as %s / %s or %s / %s\n", adminName, adminPass, memberName, memberPass)
}

func seed(st *store.Store) error {
	users, err := st.Users()
	if err != nil {
		return err
	}
	if len(users) != 1 {
		return fmt.Errorf("expected one admin, got %d", len(users))
	}
	chen := users[0]
	zhou, err := st.CreateUser(memberName, memberPass, false)
	if err != nil {
		return err
	}

	cats, err := st.Categories()
	if err != nil {
		return err
	}
	cat := map[string]int64{}
	for _, item := range cats {
		cat[item.Kind+":"+item.Name] = item.ID
	}
	daily, err := st.DefaultActivity()
	if err != nil {
		return err
	}
	if _, err := st.UpdateActivity(daily.ID, store.Activity{
		Name: daily.Name, Budget: 800000,
	}); err != nil {
		return err
	}
	trip, err := st.CreateActivity(store.Activity{
		Name: "香港旅行", Budget: 800000, TotalBudget: 2000000,
	})
	if err != nil {
		return err
	}

	debit, err := st.CreateCard(store.Card{
		Kind: store.CardDebit, Bank: "招商银行", Last4: "8888", Network: store.NetworkUnionPay,
		Funds: []store.CardFund{
			{Currency: "CNY", Balance: 1586250},
			{Currency: "HKD", Balance: 420000},
		},
	})
	if err != nil {
		return err
	}
	credit, err := st.CreateCard(store.Card{
		Kind: store.CardCredit, Bank: "中信银行", Name: "出行", Last4: "1024", Network: store.NetworkVisa,
		Funds: []store.CardFund{{Currency: "CNY", Balance: 862000}},
	})
	if err != nil {
		return err
	}

	type row struct {
		kind, date, note string
		amount           int64
		cat, act         int64
		card             int64
		user             int64
		shared           bool
	}
	food, transit, shop := cat["expense:餐饮"], cat["expense:交通"], cat["expense:购物"]
	home, play, phone := cat["expense:居住"], cat["expense:娱乐"], cat["expense:通讯"]
	wage, side := cat["income:工资"], cat["income:兼职"]
	bills := []row{
		{store.KindIncome, "2026-07-05", "七月工资", 1800000, wage, daily.ID, debit.ID, chen.ID, false},
		{store.KindExpense, "2026-07-01", "房租", 320000, home, daily.ID, debit.ID, chen.ID, true},
		{store.KindExpense, "2026-07-06", "午饭", 8600, food, daily.ID, debit.ID, chen.ID, false},
		{store.KindExpense, "2026-07-06", "咖啡", 4200, food, daily.ID, credit.ID, zhou.ID, false},
		{store.KindExpense, "2026-07-08", "地铁", 1800, transit, daily.ID, debit.ID, chen.ID, false},
		{store.KindExpense, "2026-07-12", "日用品", 25600, shop, daily.ID, credit.ID, zhou.ID, false},
		{store.KindExpense, "2026-07-15", "话费", 3900, phone, daily.ID, debit.ID, chen.ID, false},
		{store.KindIncome, "2026-08-05", "八月工资", 1800000, wage, daily.ID, debit.ID, chen.ID, false},
		{store.KindIncome, "2026-08-20", "周末兼职", 300000, side, daily.ID, credit.ID, zhou.ID, false},
		{store.KindExpense, "2026-08-01", "房租", 320000, home, daily.ID, debit.ID, chen.ID, true},
		{store.KindExpense, "2026-08-02", "机票", 89000, transit, trip.ID, credit.ID, chen.ID, true},
		{store.KindExpense, "2026-08-03", "茶餐厅", 32000, food, trip.ID, credit.ID, chen.ID, false},
		{store.KindExpense, "2026-08-05", "手信", 56000, shop, trip.ID, credit.ID, zhou.ID, true},
		{store.KindExpense, "2026-08-06", "便利店", 4500, food, daily.ID, debit.ID, chen.ID, false},
		{store.KindIncome, "2026-09-05", "九月工资", 1800000, wage, daily.ID, debit.ID, chen.ID, false},
		{store.KindExpense, "2026-09-01", "房租", 320000, home, daily.ID, debit.ID, chen.ID, true},
		{store.KindExpense, "2026-09-10", "洗衣液", 8900, shop, daily.ID, debit.ID, chen.ID, false},
		{store.KindExpense, "2026-09-12", "电影", 12800, play, daily.ID, credit.ID, zhou.ID, true},
		{store.KindExpense, "2026-09-18", "早午饭", 6800, food, daily.ID, debit.ID, chen.ID, false},
		{store.KindExpense, "2026-09-18", "地铁", 1800, transit, daily.ID, debit.ID, zhou.ID, false},
		{store.KindExpense, "2026-09-19", "晚餐", 5200, food, daily.ID, credit.ID, zhou.ID, false},
	}
	for _, item := range bills {
		if _, err := st.CreateTransaction(store.TxInput{
			Kind: item.kind, Amount: item.amount, CategoryID: item.cat, ActivityID: item.act,
			CardID: item.card, UserID: item.user, Date: item.date, Note: item.note,
			Shared: item.shared, Currency: "CNY",
		}); err != nil {
			return fmt.Errorf("%s %s: %w", item.date, item.note, err)
		}
	}
	return nil
}
