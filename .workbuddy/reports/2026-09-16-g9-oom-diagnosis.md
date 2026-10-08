# g9 服务器卡死诊断报告

- 诊断时间：2026-09-16
- 数据来源：`diag-freeze.sh` 输出（uptime 9 天 12 小时）
- 结论：**qbittorrent-nox 内存失控 → 无 cgroup 限制 → 全局 OOM → 整机换页抖动卡死**

---

## 一、结论

卡死不是磁盘满、不是 conntrack 满、不是 Docker 容器的问题，而是：

> **`seedbox-qbittorrent-user15.service` 里的 qbittorrent-nox 单个进程堆内存涨到 6.5～7.0 GiB，把 7.8 GiB 的整机内存吃光；由于该服务没有任何 cgroup 内存上限，内核只能发起「全局 OOM」，整机在 direct reclaim + 换页抖动中失去响应。**

9 天半内发生 **29 次 OOM**（平均每 8 小时一次），而且**现在仍在循环中**：当前 `available` 只剩 290 MiB、swap 已用 616 MiB，说明 qbittorrent 刚被杀掉、正在重新往上爬。

---

## 二、证据链

### 1. 内存账本（OOM 瞬间）

| 项目 | 数值 |
|---|---|
| 物理内存总量 | 7.8 GiB |
| qbittorrent-nox（anon-rss） | **6.5 GiB / 7.0 GiB**（两次事件） |
| qbittorrent-nox（total-vm） | 7.1 GiB / 11.4 GiB |
| Docker: 1Panel-mysql-kjhC | 51 MiB |
| Docker: vertex | 7 MiB |
| Swap | 4096 MB 总量，616 MB 已用 |

**关键点：`file-rss:0kB` / `file-rss:960kB`，几乎为零。** 说明这 7 GiB 全是**匿名页（堆）**——不是 mmap 映射的种子文件，而是 libtorrent 的缓存、连接缓冲区、队列和元数据。这类占用是可以靠配置压下去的，不是"必须那么大"。

### 2. 为什么是「全局 OOM」

```
oom-kill:constraint=CONSTRAINT_NONE, ..., global_oom, task_memcg=/system.slice/seedbox-qbittorrent-user15.service
```

- `constraint=CONSTRAINT_NONE` → 该 memcg **没有设置任何内存上限**，所以内核不是"在服务内部杀一个"，而是**在整个系统里挑最大的杀**。
- `global_oom` → 同一个意思：这是全系统级 OOM，不是局部 OOM。
- `task_memcg=...` → 受害者所在的 cgroup（qbittorrent）。

两次 OOM 的受害进程都是 qbittorrent-nox，但**触发者分别是 `1panel.service` 和 `sshd-auth`**：

```
[823162.515313] sshd-auth invoked oom-killer: gfp_mask=0x140cca(GFP_HIGHUSER_MOVABLE|__GFP_COMP), order=0
```

这里有两个信息：

- `order=0` → 连**单个 4 KiB 页面**的分配都失败了，内存已经彻底见底；
- 触发者只是一个 SSH 登录 → **任何进程申请一点内存都会引爆全局 OOM**。这就是"服务莫名卡住"的机制：不是某个业务崩溃，而是整机进入内存濒死状态。

### 3. 为什么表现是「卡死」而不是「崩一下就恢复」

- 可用内存 290 MiB + swap 4 GiB → 内核被迫疯狂回收 page cache，而 page cache 正是 qBittorrent 做种 I/O 的命脉，回收掉之后磁盘 I/O 直接塌陷；
- `load average: 4.01, 7.25, 4.48` → **15 分钟均值(7.25) > 1 分钟均值(4.01)**，典型的"刚从峰值回落"，即卡死刚刚发生完；
- 4 GiB swap 在 7 GiB 级的内存需求面前太小，swap 本身也变成磁盘抖动源。

### 4. 两次快照的 VM 差异说明「无界增长」

| 事件 | total-vm | anon-rss |
|---|---|---|
| pid 642174 | 7.1 GiB | 6.5 GiB |
| pid 642329 | 11.4 GiB | 7.0 GiB |

`total-vm` 涨了 60%。如果只是固定大小的磁盘缓存，RSS 会**平台化**；这里在持续抬升，更像**配置太大 + 队列/连接无界增长**（也可能叠加版本内存问题）。

---

## 三、可以排除的因素

| 项 | 判定 |
|---|---|
| 磁盘满 | ❌ 不是。`/dev/vda3 314G 213G 89G 71%`，还有 89 G |
| Docker 容器吃内存 | ❌ 不是。两个容器合计约 58 MiB |
| conntrack 表满 | ⚠️ 看起来是 2250/262144，**但该检查实际报错了**（见第五节） |
| 磁盘 I/O 饱和 | ⚠️ 无 iostat 数据，未真正测到 |
| 温度 / 硬件 | ⚠️ 未安装 lm-sensors，无数据 |

**次要隐患（现在不是元凶，但迟早出事）**：`1Panel-mysql-kjhC` 和 `vertex` **都没有设置内存限制**，在一个只有 7.8 GiB 且已经 OOM 的机器上，这等于没有护栏。

---

## 四、修复方案

### P0 — 立刻止血：给失控服务加 cgroup 内存上限

这是把"全局 OOM（整机卡死）"降级为"局部 OOM（只杀 qbittorrent）"的关键一步。

```bash
sudo mkdir -p /etc/systemd/system/seedbox-qbittorrent-user15.service.d
sudo tee /etc/systemd/system/seedbox-qbittorrent-user15.service.d/limits.conf >/dev/null <<'EOF'
[Service]
MemoryAccounting=yes
MemoryHigh=3G
MemoryMax=4G
MemorySwapMax=512M
OOMPolicy=continue
EOF
sudo systemctl daemon-reload
sudo systemctl restart seedbox-qbittorrent-user15.service

# 验证生效
systemctl show seedbox-qbittorrent-user15.service -p MemoryMax -p MemoryHigh -p MemorySwapMax
systemd-cgtop -m --order=memory -n 1
```

- `MemoryHigh=3G`：先在 3 GiB 处**限流回收**，让 qBittorrent 自己变慢而不是一路吃到底；
- `MemoryMax=4G`：硬上限，超过就在**该服务内部**触发 OOM，只有 qbittorrent 被杀，SSH / 1Panel / 其他服务不受影响；
- `MemorySwapMax=512M`：限制它把 swap 也吃光，避免整机换页抖动。

> ⚠️ 注意：**这一步只是护栏，不是治病。** 如果 qBittorrent 的自然需求真是 7 GiB，加上限后会变成"反复被杀重启"。所以 P1 必须一起做。

如果是多用户 seedbox（服务名带 `user15`），建议对**所有** slot 统一设限：

```bash
for u in $(systemctl list-unit-files --type=service --no-legend 'seedbox-qbittorrent-*.service' | awk '{print $1}'); do
  d="/etc/systemd/system/${u}.d"; sudo mkdir -p "$d"
  printf '[Service]\nMemoryHigh=3G\nMemoryMax=4G\nMemorySwapMax=512M\n' | sudo tee "$d/limits.conf" >/dev/null
done
sudo systemctl daemon-reload
```

### P0-b — 装一个"更早动手"的守护，抢在内核 OOM 之前

内核 OOM 是**最后手段**，触发时机器已经卡了。earlyoom 会在还有余量时就杀掉最大户：

```bash
sudo apt-get install -y earlyoom
sudo sed -i 's/^EARLYOOM_ARGS=.*/EARLYOOM_ARGS="-m 8 -s 5 --avoid \x28^|sshd|systemd\x29\$"/' /etc/default/earlyoom || true
sudo systemctl enable --now earlyoom
systemctl status earlyoom --no-pager
```

（或 `sudo apt-get install -y systemd-oomd && sudo systemctl enable --now systemd-oomd`，二选一即可。）

### P1 — 压掉 qBittorrent 的内存胃口（真正的病根）

WebUI → 选项，重点四项：

| 位置 | 项目 | 建议值 | 理由 |
|---|---|---|---|
| 连接 | 全局最大连接数 | 500（原值可能上千） | 每条连接都有读写缓冲区 |
| 连接 | 每个种子的最大连接数 | 50 | 同上，乘数放大 |
| 连接 | 全局 / 每种子 上传槽 | 20 / 4 | 上传槽同样是连接 |
| 高级 | 磁盘缓存 | 固定 **256 MiB**（不要设 -1/自动） | 自动模式下缓存会跟着压力膨胀，是 7 GiB 堆的主嫌 |
| 高级 | 异步 I/O 线程数 | 4（默认） | 调大反而放大缓冲 |
| 队列 | 最大活动下载 / 上传 / 活动种子数 | 按需收紧 | 每个活动种子都持有 piece map 与 announce 队列 |

然后看 WebUI → **统计** 里的内存拆分（写缓存 / 读缓存 / peer 队列等），确认哪一项是大头，再针对性收紧。

**同时务必记录版本**，必要时升降级——libtorrent 2.0.x 有若干已知的内存增长问题：

```bash
qbittorrent-nox -v
dpkg -l | grep -iE 'qbittorrent|libtorrent'
```

### P1-b — 加缓冲层（换页更快，抗突发）

```bash
sudo apt-get install -y zram-tools
sudo sed -i 's/^#\?ALGO=.*/ALGO=zstd/; s/^#\?PERCENT=.*/PERCENT=50/' /etc/default/zramswap
sudo systemctl restart zramswap
zramctl
```

zram 用压缩换页代替磁盘 swap，比 `fallocate` 一个 8 G swapfile 更能防"卡死"（磁盘 swap 本身就是卡死来源）。

### P2 — 给 Docker 容器补护栏

```bash
docker update --memory 1g  --memory-swap 1g  1Panel-mysql-kjhC
docker update --memory 512m --memory-swap 512m vertex
docker inspect -f '{{.Name}} {{.HostConfig.Memory}}' 1Panel-mysql-kjhC vertex
```

（长期应写进对应 compose 文件的 `mem_limit`，`docker update` 重启后才会保留。）

### P3 — 取证：确认是"配置性峰值"还是"无界泄漏"

```bash
# 当前大户
ps -eo pid,user,rss,vsz,comm --sort=-rss | head -15

# 按 cgroup 看内存
systemd-cgtop -m --order=memory -n 1

# OOM 历史
sudo dmesg -T | grep -E 'oom-kill|Out of memory' | tail -20
journalctl -k --since "9 days ago" | grep -c "Out of memory"

# 趋势记录：每 5 分钟采一次 RSS，判断是否单调上升
sudo tee /usr/local/bin/qbit-rss.sh >/dev/null <<'EOF'
#!/bin/bash
rss=$(ps -o rss= -C qbittorrent-nox | awk '{s+=$1} END {print int(s/1024)}')
avail=$(free -m | awk '/Mem:/{print $7}')
echo "$(date '+%F %T') rss=${rss}MB avail=${avail}MB" >> /var/log/qbit-rss.log
EOF
sudo chmod +x /usr/local/bin/qbit-rss.sh
echo '*/5 * * * * root /usr/local/bin/qbit-rss.sh' | sudo tee /etc/cron.d/qbit-rss

# 服务日志
journalctl -u seedbox-qbittorrent-user15 --since "2 days ago" -n 100 --no-pager
```

判读方法：如果 RSS 在**没有新增任务**时也持续单调上升，就是泄漏（考虑升降级 qBittorrent / libtorrent）；如果有明显的"涨到阈值就回落"，就是配置过大（按 P1 收紧）。

---

## 五、⚠️ 顺带发现：`diag-freeze.sh` 有 bug，第 6/7/8 节的 ✅ 是假阴性

```
./diag-freeze.sh: line 126: [: 0
0: integer expression expected
./diag-freeze.sh: line 142: [: 0
0: integer expression expected
./diag-freeze.sh: line 154: [: 0
0: integer expression expected
```

报错形态是 `[: 0` 换行 `0: integer expression expected` —— 说明参与比较的变量里含**两行**内容（`"0\n0"`），`[` 拿到多值就报 `integer expression expected`。

最可能的原因：`grep -c PATTERN <多个文件>` —— **grep -c 在传入多个文件时会每个文件输出一行计数**，于是变量变成两行。

同时，诊断汇总里 `任务挂起: 0` 和 `0 次` 各打印了两遍，也是同一个多行变量导致的。

**后果：conntrack 满、文件系统错误、任务挂起这三项检查实际上没有生效**，那些 ✅ 不能作为"没问题"的依据。建议改成：

```bash
# 方式一：只对一个文件计数
COUNT=$(grep -c "pattern" /var/log/kern.log 2>/dev/null)

# 方式二：多文件求和（推荐）
COUNT=$(grep -ch "pattern" /var/log/kern.log /var/log/syslog 2>/dev/null | awk '{s+=$1} END{print s+0}')

# 方式三：日志用 journalctl 统一收口，最干净
COUNT=$(journalctl -k --since "1 day ago" 2>/dev/null | grep -c "pattern")
```

修完后建议顺手把第 2 节的判据也改掉：`Swap 已用 616MB → ✅ 正常` 这个结论是错的——在 7.8 GiB 内存 + 7 GiB 需求的前提下，4 GiB swap 根本不算"正常"，应该按 `Swap 已用 / 总量 > 50%` 报警。

---

## 六、优先级汇总

| 优先级 | 动作 | 预期效果 |
|---|---|---|
| P0 | 给 qbittorrent 服务加 `MemoryMax=4G` | 全局 OOM → 局部 OOM，**整机不再卡死** |
| P0-b | 装 earlyoom | 内核 OOM 之前先动手，避免进入濒死状态 |
| P1 | 收紧连接数 / 磁盘缓存 / 活动种子数 | 从源头把 7 GiB 压到 3 GiB 以内 |
| P1-b | 上 zram | 抗突发，减少磁盘 swap 抖动 |
| P2 | 给 mysql / vertex 加内存限制 | 消除次要隐患 |
| P3 | 修 `diag-freeze.sh` + 建 RSS 趋势监控 | 让下一次诊断有可信数据 |
