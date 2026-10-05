# environment-lighting-renderer-go

一个可复现的离线环境光照渲染器。场景由程序化描述生成，光照来自本地
HDR 环境贴图（equirectangular，PFM 格式），结果输出为 PNG。渲染是渐进式、
可分块、可取消、可断点续渲的，并且在任意线程数下输出逐像素一致。

## 构建与使用

```sh
go build ./cmd/elr

# 1. 生成一张程序化天空环境贴图（本地 PFM 文件）
./elr gen-env -out sky.pfm -w 512 -h 256 -sun 0.3,0.6,-1 -sun-radiance 40

# 2. 渲染（程序化场景 + 环境光照）
./elr render -env sky.pfm -out out.png \
    -w 640 -h 360 -samples 32 -workers 8 -tile 16 \
    -checkpoint out.ckpt -preview preview.png \
    -exposure 1.0 -tonemap reinhard

# 中断后续渲（结果与一次性渲染逐像素一致）
./elr render -env sky.pfm -out out.png -samples 32 -checkpoint out.ckpt -resume
```

### CLI 配置项

| 参数 | 默认 | 说明 |
|---|---|---|
| `-env` | sky.pfm | 环境贴图（PFM，equirectangular HDR） |
| `-out` | out.png | 最终输出 PNG（原子写入） |
| `-w` / `-h` | 640 / 360 | 图像尺寸 |
| `-samples` | 16 | 每像素采样轮次（每轮 1 个相机样本 × `-light-samples` 个光照样本） |
| `-light-samples` | 1 | 每轮每像素的余弦加权光照样本数 |
| `-seed` | 1234 | 主随机种子（决定全部采样） |
| `-scene-seed` / `-spheres` | 7 / 300 | 程序化场景种子与球体数量 |
| `-workers` | NumCPU | 并行 worker 数；**不影响输出内容** |
| `-tile` | 16 | 分块边长（像素），负载均衡粒度 |
| `-timeout` | 0 | 最长渲染时间（如 `30s`），0 为不限 |
| `-checkpoint` | 空 | 每完成一轮原子写入的断点文件 |
| `-resume` | false | 从断点继续渲染 |
| `-preview` | 空 | 每轮后原子写入的渐进预览 PNG |
| `-exposure` | 1 | 色调映射前的线性曝光倍率 |
| `-tonemap` | reinhard | 映射算子：`reinhard` / `clamp` / `aces` |
| `-cam` / `-look` / `-fov` | `0,6,16` / `0,1,0` / 50 | 针孔相机参数 |

## 采样模型

- **相机**：针孔模型，每轮每像素一条抖动（jittered）主射线。
- **着色**：Lambert 漫反射 + 环境贴图直接光照。对法线半球做余弦加权
  采样，估计量为

  ```
  L_o = albedo * (1/N) * Σ_i L_env(dir_i) * visible(dir_i)
  ```

  因为漫反射 BRDF = albedo/π 与余弦采样 pdf = cos/π 相消。遮挡由
  BVH 阴影射线判定，射线原点沿法线偏移 `ShadowEpsilon = 1e-6` 以避免
  自相交。
- **未命中**：主射线直接采样环境贴图（双线性插值，u 环绕、v 极点钳制）。

### 可复现性（确定性模型）

每个 (像素, 轮次) 对拥有独立的 splitmix64 随机流，种子为
`hash(Seed, pixelIndex, round)`：

- 输出只取决于 `Config` 中与结果相关的字段（尺寸、采样数、种子等）与
  场景/贴图输入，**与 worker 数、调度顺序、是否中途取消续渲无关**；
- 每个像素的累加严格按轮次顺序进行，浮点累加顺序固定，结果逐位一致；
- 断点续渲从已完成的轮次继续，不会重复叠加或漏掉任何一轮。

## 场景组织与增量更新

- 场景物体（球、三角形）由 **BVH** 组织；全量构建使用 binned-SAH
  自上而下划分。
- **增量更新**：`Insert`（SAH 代价下降 + 叶节点嫁接）、`Remove`
  （叶摘除 + 父节点坍缩）、`Replace`（删+插），更新后沿祖先路径重新
  拟合包围盒，不做全量重建。
- **拓扑无关的正确性**：最近命中在 t 值完全相等时按图元 id 决胜，
  因此增量更新后的树与全量重建的树对所有查询返回相同结果
  （由 `TestIncrementalUpdateEqualsRebuild` 与渲染级
  `TestRenderAfterIncrementalUpdateEqualsRebuild` 验证）。
- **快照隔离**：`Scene` 用读写锁串行化渲染（读）与更新（写）。渲染
  期间持有读锁，看到的是一致快照；更新期间开始的渲染会阻塞到更新
  完成，不会读到半更新的结构。
- 注意：`Rebuild` 会为传入图元重新分配 id，传入的图元不得与另一个
  存活 BVH 共享；需要时先用 `scene.ClonePrims` 复制。

## 渐进渲染、取消与断点

- 渲染按轮次渐进：每轮为所有像素累加一个采样批次，轮次间写断点
  （`-checkpoint`）与预览（`-preview`）。
- 取消（SIGINT/SIGTERM、`-timeout` 或 context 取消）以分块为粒度生效；
  被中断的轮次整体丢弃，已完成的轮次结果保持一致。
- **原子输出**：PNG 与断点都先写临时文件再 rename，中断不会留下
  半写入文件。已完成进度通过断点文件与 `Result.RoundsDone` 区分。
- 断点文件包含配置指纹（尺寸、采样数、光照样本数、种子）；以不同
  参数 `-resume` 会报错而不是静默污染累加结果。

## 负载均衡

所有分块放入队列，worker 通过原子计数器动态领取（工作窃取）。慢分块
只占用它所在的 worker，其余 worker 继续领取新分块。`-tile` 控制粒度：
更小的分块均衡更好、调度开销略增。实测在 4 核机器上 1→2→4→8→16
线程耗时单调不增（见 `go test -bench` 与 CLI 计时）。

## HDR → 显示范围转换

`image.TonemapParams{Exposure, Gamma, Operator}`：

- `Exposure ≤ 0` 视为 1，`Gamma ≤ 0` 视为 2.2，未知算子回退 `reinhard`。
- 算子：`clamp`（硬截断）、`reinhard`（x/(1+x)）、`aces`（Stephen Hill
  拟合曲线）。所有算子在输入无界增大时饱和到白色。
- **极端值的确定行为**：`NaN` → 黑，`+Inf` → 白，`-Inf` 与负值 → 黑；
  有限超大值（如 1e300）经映射饱和为白。该行为对三个算子一致，
  由 `TestTonemapExtremes` 锁定。

## 环境贴图格式

使用 PFM（Portable Float Map）：小端、彩色（`PF`）。文件首行为底部行
（PFM 约定），内部第 0 行为天顶（+Y）。`elr gen-env` 可生成程序化天空
（地平线-天顶渐变 + 太阳盘）。

## 测试

```sh
go test ./...        # 单元与集成测试
go test -race ./...  # 竞态检测
```

覆盖：BVH 对暴力扫描的正确性（无漏遮挡/穿模/自相交）、增量更新与全量
重建一致性（查询级与渲染级）、多线程/重复运行逐像素一致、取消与断点
恢复等于一次性渲染、超时不留半成品文件、断点配置不匹配报错、极端
亮度边界、PFM 读写往返、原子写不遗留临时文件。

## 目录结构

```
geom/     向量、射线、AABB
scene/    图元求交、BVH（增量更新）、程序化场景、快照隔离
envmap/   PFM 读写、equirectangular 采样、程序化天空
image/    HDR 累加缓冲、色调映射、原子 PNG/文件写入
render/   确定性采样、分块调度、渐进渲染、断点续渲
cmd/elr/  命令行入口
```
