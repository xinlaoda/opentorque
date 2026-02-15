# TORQUE Job Queue 管理详细分析

## 1. 概述

在TORQUE系统中，**队列(Queue)** 是作业管理的核心概念。所有提交的作业必须进入某个队列，由队列管理作业的排队、调度策略和资源限制。队列运行在 **pbs_server** 上，是一个逻辑概念，用于组织和管理作业。

---

## 2. 队列类型

TORQUE支持两种基本队列类型：

### 2.1 执行队列 (Execution Queue)

```c
#define QTYPE_Execution 1
```

- 作业在此队列中等待被调度执行
- 作业直接从此队列分发到计算节点
- 支持资源限制、ACL控制、检查点等功能
- **最常用的队列类型**

### 2.2 路由队列 (Routing Queue)

```c
#define QTYPE_RoutePush 2   // Push模式
#define QTYPE_RoutePull 3   // Pull模式
```

- 作业在此队列中不会执行
- 根据配置的路由规则将作业转发到其他队列
- 用于实现复杂的作业分发策略
- 可以路由到本地或远程服务器的队列

---

## 3. 队列数据结构

### 3.1 核心结构定义

```c
// 队列固定信息 (持久化)
typedef struct _queuefix {
    int    qu_modified;                    // 修改标志
    int    qu_type;                        // 队列类型
    time_t qu_ctime;                       // 创建时间
    time_t qu_mtime;                       // 修改时间
    char   qu_name[PBS_MAXQUEUENAME+1];    // 队列名称 (最长15字符)
} queuefix;

// 完整队列结构
typedef struct pbs_queue {
    int              q_being_recycled;     // 回收标志
    all_jobs        *qu_jobs;              // 队列中的作业
    all_jobs        *qu_jobs_array_sum;    // 作业数组汇总
    
    queuefix         qu_qs;                // 固定信息
    
    pthread_mutex_t *qu_mutex;             // 队列互斥锁
    
    int              qu_numjobs;           // 当前作业数
    int              qu_njstate[PBS_NUMJOBSTATE];  // 各状态作业数
    char             qu_jobstbuf[100];     // 状态缓冲
    
    pbs_attribute    qu_attr[QA_ATR_LAST]; // 属性数组
    
    user_info_holder *qu_uih;              // 用户信息
    pthread_t        route_retry_thread_id;// 路由重试线程
    int              qu_reserved_jobs;     // 预留作业数
} pbs_queue;
```

---

## 4. 队列属性完整列表

### 4.1 通用属性 (所有队列类型)

| 属性名 | 宏定义 | 类型 | 描述 |
|--------|--------|------|------|
| `queue_type` | `QA_ATR_QType` | string | 队列类型 (e=执行, r=路由) |
| `priority` | `QA_ATR_Priority` | long | 队列优先级 |
| `hostlist` | `QA_ATR_Hostlist` | ACL | 关联的计算节点列表 |
| `restartable` | `QA_ATR_Rerunnable` | bool | 是否可重运行 |
| `max_queuable` | `QA_ATR_MaxJobs` | long | 最大排队作业数 |
| `max_user_queuable` | `QA_ATR_MaxUserJobs` | long | 每用户最大排队数 |
| `total_jobs` | `QA_ATR_TotalJobs` | long | 当前作业总数 (只读) |
| `state_count` | `QA_ATR_JobsByState` | string | 各状态作业数 (只读) |
| `max_report` | `QA_ATR_MaxReport` | long | 最大报告数 |
| `max_running` | `QA_ATR_MaxRun` | long | 最大同时运行作业数 |
| `enabled` | `QA_ATR_Enabled` | bool | 队列是否启用 |
| `started` | `QA_ATR_Started` | bool | 队列是否已启动 |
| `mtime` | `QA_ATR_MTime` | long | 修改时间 (只读) |
| `from_route_only` | `QA_ATR_FromRouteOnly` | bool | 只接受路由来的作业 |
| `disallowed_types` | `QA_ATR_DisallowedTypes` | ACL | 禁止的作业类型 |
| `features_required` | `QA_ATR_FeaturesRequired` | string | 所需节点特性 |
| `required_login_property` | `QA_ATR_ReqLoginProperty` | string | 登录节点属性要求 |
| `ghost_queue` | `QA_ATR_GhostQueue` | bool | 幽灵队列标志 |

### 4.2 资源限制属性

| 属性名 | 宏定义 | 描述 |
|--------|--------|------|
| `resources_max` | `QA_ATR_ResourceMax` | 最大资源限制 |
| `resources_min` | `QA_ATR_ResourceMin` | 最小资源要求 |
| `resources_default` | `QA_ATR_ResourceDefault` | 默认资源值 |
| `req_information_max` | `QA_ATR_ReqInformationMax` | 最大请求信息 |
| `req_information_min` | `QA_ATR_ReqInformationMin` | 最小请求信息 |
| `req_information_default` | `QA_ATR_ReqInformationDefault` | 默认请求信息 |

### 4.3 访问控制列表 (ACL) 属性

| 属性名 | 启用属性 | 描述 |
|--------|----------|------|
| `acl_hosts` | `acl_host_enable` | 允许提交的主机列表 |
| `acl_users` | `acl_user_enable` | 允许提交的用户列表 |
| `acl_groups` | `acl_group_enable` | 允许提交的组列表 |
| `acl_logic_or` | - | ACL逻辑OR (用户或组通过即可) |
| `acl_group_sloppy` | - | 宽松组检查 |

### 4.4 执行队列专用属性

| 属性名 | 宏定义 | 描述 |
|--------|--------|------|
| `checkpoint_dir` | `QE_ATR_checkpoint_dir` | 检查点目录 |
| `checkpoint_min` | `QE_ATR_checkpoint_min` | 最小检查点间隔 |
| `checkpoint_defaults` | `QE_ATR_checkpoint_defaults` | 默认检查点策略 |
| `rendezvous_retry` | `QE_ATR_RendezvousRetry` | 同步重试次数 |
| `reserved_expedite` | `QE_ATR_ReservedExpedite` | 加速预留 |
| `reserved_sync` | `QE_ATR_ReservedSync` | 同步预留 |
| `resources_available` | `QE_ATR_ResourceAvail` | 可用资源 |
| `resources_assigned` | `QE_ATR_ResourceAssn` | 已分配资源 (只读) |
| `kill_delay` | `QE_ATR_KillDelay` | 终止延迟时间 |
| `max_user_run` | `QE_ATR_MaxUserRun` | 每用户最大运行数 |
| `max_group_run` | `QE_ATR_MaxGrpRun` | 每组最大运行数 |
| `keep_completed` | `QE_ATR_KeepCompleted` | 保留完成作业时间(秒) |
| `is_transit` | `QE_ATR_is_transit` | 是否为中转队列 |

### 4.5 路由队列专用属性

| 属性名 | 宏定义 | 描述 |
|--------|--------|------|
| `route_destinations` | `QR_ATR_RouteDestin` | 路由目标队列列表 |
| `alt_router` | `QR_ATR_AltRouter` | 使用备用路由器 |
| `route_held_jobs` | `QR_ATR_RouteHeld` | 是否路由挂起作业 |
| `route_waiting_jobs` | `QR_ATR_RouteWaiting` | 是否路由等待作业 |
| `route_retry_time` | `QR_ATR_RouteRetryTime` | 路由重试间隔 |
| `route_lifetime` | `QR_ATR_RouteLifeTime` | 路由生命周期 |

### 4.6 禁止的作业类型

```c
char *array_disallowed_types[] = {
    "batch",            // 批处理作业
    "interactive",      // 交互式作业
    "rerunable",        // 可重运行作业
    "nonrerunable",     // 不可重运行作业
    "fault_tolerant",   // 容错作业
    "fault_intolerant", // 非容错作业
    "job_array",        // 作业数组
};
```

---

## 5. 队列配置与管理

### 5.1 使用qmgr命令管理队列

#### 创建队列

```bash
# 创建执行队列
qmgr -c "create queue batch queue_type=execution"

# 创建路由队列
qmgr -c "create queue submit queue_type=route"
```

#### 配置队列属性

```bash
# 启用队列
qmgr -c "set queue batch enabled=true"
qmgr -c "set queue batch started=true"

# 设置资源限制
qmgr -c "set queue batch resources_max.walltime=24:00:00"
qmgr -c "set queue batch resources_max.ncpus=64"
qmgr -c "set queue batch resources_max.mem=128gb"

# 设置默认资源
qmgr -c "set queue batch resources_default.walltime=01:00:00"
qmgr -c "set queue batch resources_default.ncpus=1"

# 设置作业限制
qmgr -c "set queue batch max_queuable=1000"
qmgr -c "set queue batch max_running=100"
qmgr -c "set queue batch max_user_run=10"

# 设置访问控制
qmgr -c "set queue batch acl_user_enable=true"
qmgr -c "set queue batch acl_users=user1,user2,@group1"

# 设置主机列表 (关联计算节点)
qmgr -c "set queue batch hostlist=node01,node02,node03"

# 配置路由队列
qmgr -c "set queue submit route_destinations=batch,long,short"
qmgr -c "set queue submit route_lifetime=300"
```

#### 查看队列

```bash
# 查看所有队列
qmgr -c "list queue @default"

# 查看特定队列详细信息
qmgr -c "print queue batch"

# 使用qstat查看队列状态
qstat -Q              # 队列摘要
qstat -Qf             # 队列详细信息
qstat -Qf batch       # 特定队列详情
```

#### 删除队列

```bash
# 先禁用并清空队列
qmgr -c "set queue batch enabled=false"
qmgr -c "set queue batch started=false"

# 删除队列
qmgr -c "delete queue batch"
```

### 5.2 典型队列配置示例

```bash
#!/bin/bash
# 创建典型的HPC队列配置

# 1. 短作业队列
qmgr -c "create queue short queue_type=execution"
qmgr -c "set queue short enabled=true"
qmgr -c "set queue short started=true"
qmgr -c "set queue short resources_max.walltime=01:00:00"
qmgr -c "set queue short resources_max.ncpus=16"
qmgr -c "set queue short max_running=50"
qmgr -c "set queue short priority=100"

# 2. 普通作业队列
qmgr -c "create queue normal queue_type=execution"
qmgr -c "set queue normal enabled=true"
qmgr -c "set queue normal started=true"
qmgr -c "set queue normal resources_max.walltime=24:00:00"
qmgr -c "set queue normal resources_max.ncpus=64"
qmgr -c "set queue normal max_running=100"
qmgr -c "set queue normal priority=50"

# 3. 长作业队列
qmgr -c "create queue long queue_type=execution"
qmgr -c "set queue long enabled=true"
qmgr -c "set queue long started=true"
qmgr -c "set queue long resources_max.walltime=168:00:00"
qmgr -c "set queue long resources_max.ncpus=256"
qmgr -c "set queue long max_running=20"
qmgr -c "set queue long priority=10"

# 4. GPU队列
qmgr -c "create queue gpu queue_type=execution"
qmgr -c "set queue gpu enabled=true"
qmgr -c "set queue gpu started=true"
qmgr -c "set queue gpu hostlist=gpu01,gpu02,gpu03,gpu04"
qmgr -c "set queue gpu resources_max.walltime=48:00:00"

# 5. 设置默认队列
qmgr -c "set server default_queue=normal"
```

---

## 6. 队列与计算节点的关系

### 6.1 关系模型

```
┌─────────────────────────────────────────────────────────────────┐
│                        pbs_server                                │
│                                                                  │
│   ┌─────────────┐  ┌─────────────┐  ┌─────────────┐            │
│   │ Queue:short │  │Queue:normal │  │ Queue:gpu   │            │
│   │             │  │             │  │             │            │
│   │ hostlist:   │  │ hostlist:   │  │ hostlist:   │            │
│   │ node[01-10] │  │ node[01-50] │  │ gpu[01-04]  │            │
│   └──────┬──────┘  └──────┬──────┘  └──────┬──────┘            │
│          │                │                │                    │
└──────────┼────────────────┼────────────────┼────────────────────┘
           │                │                │
           ▼                ▼                ▼
    ┌──────────────────────────────────────────────────────┐
    │                   计算节点集群                         │
    │                                                       │
    │  ┌────────┐ ┌────────┐ ┌────────┐     ┌────────┐    │
    │  │node01  │ │node02  │ │node03  │ ... │node50  │    │
    │  │pbs_mom │ │pbs_mom │ │pbs_mom │     │pbs_mom │    │
    │  └────────┘ └────────┘ └────────┘     └────────┘    │
    │                                                       │
    │  ┌────────┐ ┌────────┐ ┌────────┐ ┌────────┐        │
    │  │ gpu01  │ │ gpu02  │ │ gpu03  │ │ gpu04  │        │
    │  │pbs_mom │ │pbs_mom │ │pbs_mom │ │pbs_mom │        │
    │  │(GPU节点)│ │(GPU节点)│ │(GPU节点)│ │(GPU节点)│        │
    │  └────────┘ └────────┘ └────────┘ └────────┘        │
    │                                                       │
    └──────────────────────────────────────────────────────┘
```

### 6.2 节点关联方式

#### 方式1: hostlist属性 (TORQUE特有)

```bash
# 将特定节点关联到队列
qmgr -c "set queue gpu hostlist=gpu01,gpu02,gpu03,gpu04"

# 使用通配符
qmgr -c "set queue compute hostlist=node[001-100]"
```

#### 方式2: 节点属性匹配

```bash
# 设置队列所需的节点特性
qmgr -c "set queue gpu features_required=gpu"

# 在节点上设置properties
qmgr -c "set node gpu01 properties=gpu,tesla"
```

#### 方式3: 依赖调度器

- 调度器(如Maui/Moab)可以根据更复杂的规则将作业分配到节点
- 队列可以不直接关联节点，由调度器决定

### 6.3 作业提交与节点选择流程

```
用户提交作业
     │
     ▼
┌────────────────┐
│ 选择目标队列    │  qsub -q <queue_name>
│ (默认或指定)    │  或使用默认队列
└───────┬────────┘
        │
        ▼
┌────────────────┐
│ svr_chkque()   │  检查队列准入条件
│ 队列准入检查    │  - ACL检查
│                │  - 资源限制检查
│                │  - 作业类型检查
└───────┬────────┘
        │
        ▼
┌────────────────┐
│ 作业入队        │  作业进入队列等待
│ (queued状态)   │
└───────┬────────┘
        │
        ▼
┌────────────────┐
│ 调度器调度      │  pbs_sched 选择作业
│                │  根据优先级、资源等
└───────┬────────┘
        │
        ▼
┌────────────────┐
│ 节点选择        │  根据以下因素:
│                │  - 队列hostlist
│                │  - 作业资源需求
│                │  - 节点可用性
│                │  - features匹配
└───────┬────────┘
        │
        ▼
┌────────────────┐
│ 分发到MOM      │  QueueJob → Commit
│ 作业执行        │  在计算节点上运行
└────────────────┘
```

---

## 7. 队列准入检查 (svr_chkque)

当作业尝试进入队列时，系统执行以下检查：

### 7.1 检查流程

```c
int svr_chkque(job *pjob, pbs_queue *pque, char *hostname, int mtype, char *EMsg)
{
    // 1. 对于执行队列的额外检查
    if (pque->qu_qs.qu_type == QTYPE_Execution) {
        // 1a. 检查执行UID/GID是否可建立
        check_execution_uid_and_gid(pjob, EMsg);
        
        // 1b. 站点ACL检查
        site_acl_check(pjob, pque);
        
        // 1c. 检查是否有未知资源请求
        find_resc_entry(...);
        
        // 1d. 检查是否有未知属性
        
        // 1e. 检查队列禁止的作业类型
        check_queue_disallowed_types(pjob, pque, EMsg);
        
        // 1f. 组ACL检查
        check_queue_group_ACL(pjob, pque, mtype, EMsg);
    }
    
    // 2. 队列启用状态检查 (非管理员移动)
    check_queue_enabled(pjob, pque, EMsg);
    
    // 3. 队列作业数量限制检查
    check_queue_job_limit(pjob, pque, EMsg);
    
    // 4. from_route_only检查
    check_local_route(pjob, pque, mtype, EMsg);
    
    // 5. 主机ACL检查
    check_queue_host_ACL(pjob, pque, hostname, EMsg);
    
    // 6. 用户ACL检查
    check_queue_user_ACL(pjob, pque, EMsg);
    
    // 7. 资源限制检查
    are_job_resources_in_limits_of_queue(pjob, pque, EMsg);
    
    return PBSE_NONE;  // 准入成功
}
```

### 7.2 检查类型说明

| 检查项 | 说明 | 绕过条件 |
|--------|------|----------|
| 队列启用检查 | enabled=true | 管理员移动、qorder |
| 作业数量限制 | max_queuable | 管理员移动、qorder |
| 路由限制 | from_route_only | 管理员移动、qorder |
| 主机ACL | acl_host_enable | 管理员移动 |
| 用户ACL | acl_user_enable | 管理员移动 |
| 组ACL | acl_group_enable | 管理员移动 |
| 资源限制 | resources_max/min | 管理员移动 |
| 禁止类型 | disallowed_types | 无 |

---

## 8. 路由队列详解

### 8.1 路由机制

```
┌─────────────────────────────────────────────────────────────────┐
│                     路由队列工作流程                              │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│   用户 ──qsub──> 路由队列(submit)                                │
│                       │                                          │
│                       ▼                                          │
│              ┌────────────────┐                                  │
│              │ 检查路由规则    │                                  │
│              │ route_destinations                                │
│              └───────┬────────┘                                  │
│                      │                                           │
│         ┌────────────┼────────────┐                              │
│         │            │            │                              │
│         ▼            ▼            ▼                              │
│   ┌──────────┐ ┌──────────┐ ┌──────────┐                        │
│   │ 目标队列1 │ │ 目标队列2 │ │ 目标队列3 │                        │
│   │ (short)  │ │ (normal) │ │ (long)   │                        │
│   └──────────┘ └──────────┘ └──────────┘                        │
│                                                                  │
│   路由逻辑: 按顺序尝试，第一个接受作业的队列获胜                    │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

### 8.2 路由队列配置示例

```bash
# 创建路由队列
qmgr -c "create queue submit queue_type=route"
qmgr -c "set queue submit enabled=true"
qmgr -c "set queue submit started=true"

# 设置路由目标 (按顺序尝试)
qmgr -c "set queue submit route_destinations=short,normal,long"

# 路由参数
qmgr -c "set queue submit route_lifetime=300"      # 最大路由时间300秒
qmgr -c "set queue submit route_retry_time=60"     # 重试间隔60秒
qmgr -c "set queue submit route_held_jobs=false"   # 不路由挂起作业
qmgr -c "set queue submit route_waiting_jobs=true" # 路由等待作业
```

### 8.3 路由返回值

```c
#define ROUTE_PERM_FAILURE  -1  // 永久失败，作业将被删除
#define ROUTE_SUCCESS        0  // 路由成功
#define ROUTE_RETRY          1  // 临时失败，将重试
#define ROUTE_DEFERRED       2  // 延迟路由
```

---

## 9. 作业生命周期与队列

### 9.1 作业状态

```
┌──────────────────────────────────────────────────────────────┐
│                      作业状态转换                              │
├──────────────────────────────────────────────────────────────┤
│                                                               │
│   [Transit] ──> [Queued] ──> [Running] ──> [Exiting] ──> [Complete]
│       │            │             │             │               │
│       │            │             │             │               │
│       │            ▼             ▼             │               │
│       │         [Held]       [Suspend]         │               │
│       │            │             │             │               │
│       │            └──────┬──────┘             │               │
│       │                   │                    │               │
│       │                   ▼                    │               │
│       └────────────> [Waiting] ────────────────┘               │
│                                                               │
└──────────────────────────────────────────────────────────────┘

状态说明:
- Transit (T): 正在传输/路由
- Queued  (Q): 排队等待
- Held    (H): 被挂起
- Waiting (W): 等待依赖/时间
- Running (R): 正在运行
- Exiting (E): 正在退出
- Complete(C): 已完成
- Suspend (S): 被暂停
```

### 9.2 队列中的作业管理

```c
// 队列中作业状态统计
struct pbs_queue {
    int qu_numjobs;                          // 总作业数
    int qu_njstate[PBS_NUMJOBSTATE];         // 各状态作业数
    // qu_njstate[JOB_STATE_QUEUED]  - 排队中
    // qu_njstate[JOB_STATE_RUNNING] - 运行中
    // qu_njstate[JOB_STATE_HELD]    - 被挂起
    // ...
};
```

---

## 10. 队列持久化

### 10.1 存储位置

```
$PBS_HOME/server_priv/queues/
├── batch           # 队列配置文件
├── short
├── normal
├── long
└── gpu
```

### 10.2 队列恢复

pbs_server启动时从 `$PBS_HOME/server_priv/queues/` 目录读取所有队列配置并恢复。

---

## 11. 最佳实践

### 11.1 队列设计建议

1. **按作业特性分类**
   - 短作业队列 (walltime < 1h)
   - 普通作业队列 (walltime 1-24h)
   - 长作业队列 (walltime > 24h)

2. **按资源需求分类**
   - CPU密集型队列
   - 内存密集型队列
   - GPU队列
   - 大规模并行队列

3. **按用户/项目分类**
   - 开发调试队列
   - 生产队列
   - 特定项目队列

### 11.2 资源限制策略

```bash
# 合理设置资源上下限
qmgr -c "set queue batch resources_max.walltime=168:00:00"
qmgr -c "set queue batch resources_min.walltime=00:01:00"
qmgr -c "set queue batch resources_default.walltime=01:00:00"

# 防止资源滥用
qmgr -c "set queue batch max_user_run=50"
qmgr -c "set queue batch max_group_run=100"
```

### 11.3 访问控制策略

```bash
# 严格的ACL控制
qmgr -c "set queue secure acl_user_enable=true"
qmgr -c "set queue secure acl_users=admin,power_users"
qmgr -c "set queue secure acl_host_enable=true"
qmgr -c "set queue secure acl_hosts=submit.cluster.local"
```

---

## 12. 常用命令参考

| 命令 | 功能 |
|------|------|
| `qmgr -c "create queue <name> queue_type=<type>"` | 创建队列 |
| `qmgr -c "delete queue <name>"` | 删除队列 |
| `qmgr -c "set queue <name> <attr>=<value>"` | 设置属性 |
| `qmgr -c "unset queue <name> <attr>"` | 取消属性 |
| `qmgr -c "list queue <name>"` | 列出属性 |
| `qmgr -c "print queue <name>"` | 打印配置 |
| `qstat -Q` | 队列摘要 |
| `qstat -Qf` | 队列详情 |
| `qstat -q` | 队列作业统计 |
| `qenable <queue>` | 启用队列 |
| `qdisable <queue>` | 禁用队列 |
| `qstart <queue>` | 启动队列 |
| `qstop <queue>` | 停止队列 |
