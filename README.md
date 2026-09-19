# 记账

- 多用户：一家人共用一份账目，每笔能看出是谁记的，也可以标成共同账。管理员负责成员和密码。
- 灵活分类：分类可改图标、颜色、归档和顺序；支出和收入挂在活动上。
- 支持多国银行卡：银行卡按币种记余额，支持银联、Visa、Mastercard、运通。

一个 Go 静态二进制，自带网页和 SQLite，监听一个端口就能跑。界面按手机浏览器设计，也可以加到主屏。页面不加载任何外部资源；不配置识别密钥时，完全离线也能用。

<p align="center">
  <img src="docs/screenshots/ledger.png" alt="账本" width="46%">
  <img src="docs/screenshots/stats.png" alt="统计" width="46%">
</p>
<p align="center">
  <img src="docs/screenshots/entry.png" alt="记一笔" width="46%">
  <img src="docs/screenshots/settings.png" alt="我的" width="46%">
</p>

## 本地运行

需要 Go 1.27 以上。

```
make demo         # 写入两名演示成员和几个月的模拟账单，然后启动
make test         # go vet + go test
make build-linux  # 交叉编译出 dist/ledger-linux-amd64
```

演示账本的登录是 `小陈` / `demodemo`，另一位成员是 `小周` / `demodemo`。空库启动用 `make run`，会按环境变量创建第一个管理员。

## 它做什么

- 底栏三个入口：账本、记一笔、我的。金额用自定义数字键盘。
- 支出和收入挂在活动上，默认是「日常生活」；转账和兑换不算收支。
- 两名以上成员时，可以记入共同账。共同是单独一本账，不拆给各人。
- 账本首页是余额和本月有账单的活动；点开看结余和最近几笔，可以直接改。
- 统计按账本、活动和时间（月 / 年 / 全部）筛选，有分类环、银行汇总和走势。
- 银行卡按币种记余额，支持银联、Visa、Mastercard、运通；可以转账和手填兑换，不存汇率。
- 活动可以设每月上限和活动上限。上限看全家支出，不按账本拆分。
- 分类可改图标、颜色、归档和顺序。账目可导出 CSV。

## 配置

全部通过环境变量设置。服务器上这些变量放在 `/etc/ledger/env`。

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `LEDGER_ADDR` | `:18080` | 监听地址 |
| `LEDGER_DB` | `ledger.db` | SQLite 文件路径 |
| `LEDGER_ADMIN_USER` | `admin` | 首个管理员的用户名 |
| `LEDGER_ADMIN_PASSWORD` | 无 | 首个管理员的密码，至少 8 位 |
| `LEDGER_SECRET` | 无 | 会话 cookie 的签名密钥，至少 16 个字符 |
| `LEDGER_TLS_CERT` | 无 | HTTPS 证书路径 |
| `LEDGER_TLS_KEY` | 无 | HTTPS 私钥路径 |
| `LEDGER_DEEPSEEK_KEY` | 无 | DeepSeek API 密钥；留空则不出现识别入口 |
| `LEDGER_DEEPSEEK_URL` | `https://api.deepseek.com` | DeepSeek API 地址 |

`LEDGER_ADMIN_USER` 和 `LEDGER_ADMIN_PASSWORD` 只在库里还没有成员时使用。`LEDGER_SECRET` 留空会每次启动随机生成，登录状态会在重启后失效。证书成对填写才启用 HTTPS。

识别密钥只留在服务器环境变量里。只有成员主动点「短信」或「识图」才会把这段内容发到 DeepSeek。

## 部署

`deploy/server/provision.sh` 把一台 Debian 13 机器收成能跑账本的样子；`deploy/deploy.sh` 从本机编译并推送。两个脚本都可以重复执行，provision 不会覆盖已有的 `/etc/ledger/env`、证书和数据库。

先在 `~/.ssh/config` 里给服务器起个别名：

```
Host your-server
    HostName 203.0.113.10
    User root
    IdentityFile ~/.ssh/id_ed25519
    IdentitiesOnly yes
```

```
scp -r deploy/server your-server:/root/
ssh your-server 'bash /root/server/provision.sh --hostname your-server --public-ip 203.0.113.10 --admin-user 你的用户名'
deploy/deploy.sh your-server
```

自签名证书第一次打开会提示警告。日常更新再跑一次 `deploy/deploy.sh your-server`。

登录失败会写固定格式的日志，交给 fail2ban 封禁；程序自己还有一层 10 分钟 5 次的节流。

## 备份

数据在一个 SQLite 文件里。开了 WAL，复制前先停服务：

```
systemctl stop ledger
cp /var/lib/ledger/ledger.db ~/ledger-$(date +%F).db
systemctl start ledger
```

也可以在「我的」里导出 CSV。

## 许可

MIT

## AI 使用声明

目前是 Vibe Coding 项目。仓库全部由 Cursor Grok 4.6 + Cursor Harness 生成。
