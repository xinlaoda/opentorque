# pbs_mom 模块深度分析

## 1. 模块概述

**pbs_mom** (Machine Oriented Mini-server) 是TORQUE系统中运行在每个计算节点上的守护进程，负责本地作业执行、资源监控和与pbs_server的通信。

---

## 2. 服务端口配置

| 端口 | 宏定义 | 默认值 | 用途 |
|------|--------|--------|------|
| MOM服务端口 | `PBS_MOM_SERVICE_PORT` | 15002 | 接收pbs_server的作业管理请求 |
| MOM管理端口 | `PBS_MANAGER_SERVICE_PORT` | 15003 | 资源监控(RM)和外部管理工具通信 |
| TM端口 | `pbs_tm_port` | 动态 | Task Manager端口，用于作业内部任务通信 |

---

## 3. 对外提供的服务接口

### 3.1 批处理请求服务 (Batch Request Services)

pbs_mom在服务端口上接收来自pbs_server的批处理请求，定义在 `pbs_batchreqtype_db.h`：

| 请求类型 | 宏定义 | 描述 | 数据结构 |
|----------|--------|------|----------|
| **QueueJob** | `PBS_BATCH_QueueJob` | 作业入队请求 | `struct rq_queuejob` |
| **JobCred** | `PBS_BATCH_JobCred` | 作业凭证传输 | `struct rq_jobcred` |
| **JobScript** | `PBS_BATCH_jobscript` | 作业脚本传输 | `struct rq_jobfile` |
| **ReadyToCommit** | `PBS_BATCH_RdytoCommit` | 准备提交确认 | job_id字符串 |
| **Commit** | `PBS_BATCH_Commit` | 提交作业执行 | job_id字符串 |
| **DeleteJob** | `PBS_BATCH_DeleteJob` | 删除/终止作业 | job_id + extend |
| **HoldJob** | `PBS_BATCH_HoldJob` | 挂起作业 | job_id + hold_type |
| **ModifyJob** | `PBS_BATCH_ModifyJob` | 修改作业属性 | `struct rq_manage` |
| **SignalJob** | `PBS_BATCH_SignalJob` | 向作业发送信号 | `struct rq_signal` |
| **StatusJob** | `PBS_BATCH_StatusJob` | 查询作业状态 | `struct rq_status` |
| **RunJob** | `PBS_BATCH_RunJob` | 执行作业 | `struct rq_runjob` |
| **Rerun** | `PBS_BATCH_Rerun` | 重新运行作业 | job_id |
| **CheckpointJob** | `PBS_BATCH_CheckpointJob` | 检查点操作 | job_id |
| **MessageJob** | `PBS_BATCH_MessJob` | 发送消息到作业 | `struct rq_message` |
| **CopyFiles** | `PBS_BATCH_CopyFiles` | 文件复制/暂存 | `struct rq_cpyfile` |
| **ReturnFiles** | `PBS_BATCH_ReturnFiles` | 返回输出文件 | `struct rq_cpyfile` |
| **DeleteFiles** | `PBS_BATCH_DelFiles` | 删除文件 | `struct rq_cpyfile` |

### 3.2 核心请求数据结构

```c
// 作业入队请求
struct rq_queuejob {
    char     rq_destin[PBS_MAXDEST+1];      // 目标队列
    char     rq_jid[PBS_MAXSVRJOBID+1];     // 作业ID
    tlist_head rq_attr;                      // 属性列表(svrattrlist)
};

// 作业凭证
struct rq_jobcred {
    int    rq_type;                          // 凭证类型
    long   rq_size;                          // 凭证大小
    char   *rq_data;                         // 凭证数据
};

// 作业文件/脚本
struct rq_jobfile {
    int   rq_sequence;                       // 序列号
    int   rq_type;                           // 文件类型
    long  rq_size;                           // 文件大小
    char  rq_jobid[PBS_MAXSVRJOBID+1];       // 作业ID
    char  *rq_data;                          // 文件内容
};

// 运行作业请求
struct rq_runjob {
    char  rq_jid[PBS_MAXSVRJOBID+1];         // 作业ID
    char  *rq_destin;                        // 执行位置
    unsigned int rq_resch;                   // 重新调度标志
};

// 信号请求
struct rq_signal {
    char  rq_jid[PBS_MAXSVRJOBID+1];         // 作业ID
    char  rq_signame[PBS_SIGNAMESZ+1];       // 信号名称
};

// 文件复制请求
struct rq_cpyfile {
    char  rq_jobid[PBS_MAXSVRJOBID+1];       // 作业ID
    char  rq_owner[PBS_MAXUSER+1];           // 所有者
    char  rq_group[PBS_MAXGRPN+1];           // 组
    int   rq_dir;                            // 方向(输入/输出)
    tlist_head rq_pair;                      // 文件对列表
};
```

---

## 4. MOM间通信协议 (Inter-MOM Protocol)

### 4.1 IM命令 (Inter-Manager Commands)

用于Mother Superior与Sister MOM之间的通信：

| 命令 | 宏定义 | 值 | 描述 |
|------|--------|-----|------|
| ALL_OKAY | `IM_ALL_OKAY` | 0 | 操作成功确认 |
| JOIN_JOB | `IM_JOIN_JOB` | 1 | 加入作业执行 |
| KILL_JOB | `IM_KILL_JOB` | 2 | 终止作业 |
| SPAWN_TASK | `IM_SPAWN_TASK` | 3 | 创建新任务 |
| GET_TASKS | `IM_GET_TASKS` | 4 | 获取任务列表 |
| SIGNAL_TASK | `IM_SIGNAL_TASK` | 5 | 向任务发信号 |
| OBIT_TASK | `IM_OBIT_TASK` | 6 | 任务终止通知 |
| POLL_JOB | `IM_POLL_JOB` | 7 | 轮询作业状态 |
| GET_INFO | `IM_GET_INFO` | 8 | 获取信息 |
| GET_RESC | `IM_GET_RESC` | 9 | 获取资源使用 |
| ABORT_JOB | `IM_ABORT_JOB` | 10 | 中止作业 |
| GET_TID | `IM_GET_TID` | 11 | 获取任务ID |
| RADIX_ALL_OK | `IM_RADIX_ALL_OK` | 12 | Radix模式确认 |
| JOIN_JOB_RADIX | `IM_JOIN_JOB_RADIX` | 13 | Radix加入作业 |
| KILL_JOB_RADIX | `IM_KILL_JOB_RADIX` | 14 | Radix终止作业 |
| PMIx_FENCE | `IM_FENCE` | 15 | PMIx栅栏同步 |
| PMIx_CONNECT | `IM_CONNECT` | 16 | PMIx连接 |
| PMIx_DISCONNECT | `IM_DISCONNECT` | 17 | PMIx断开 |
| ERROR | `IM_ERROR` | 99 | 错误响应 |

### 4.2 IM协议格式

```
协议号: IM_PROTOCOL (3)
协议版本: IM_PROTOCOL_VER (2)

消息格式:
+----------+-----------+---------+--------+----------+
| Protocol | Version   | Command | Job ID | Payload  |
| (int)    | (int)     | (int)   | (str)  | (varies) |
+----------+-----------+---------+--------+----------+
```

---

## 5. Server-MOM通信协议 (IS Protocol)

### 5.1 IS命令 (Inter-Server Commands)

用于pbs_server与pbs_mom之间的通信：

| 命令 | 宏定义 | 值 | 方向 | 描述 |
|------|--------|-----|------|------|
| NULL | `IS_NULL` | 0 | - | 空操作 |
| CLUSTER_ADDRS | `IS_CLUSTER_ADDRS` | 2 | Server→MOM | 集群地址信息 |
| UPDATE | `IS_UPDATE` | 3 | MOM→Server | 状态更新 |
| STATUS | `IS_STATUS` | 4 | MOM→Server | 完整状态报告 |
| GPU_STATUS | `IS_GPU_STATUS` | 5 | MOM→Server | GPU状态报告 |

### 5.2 IS协议格式

```
协议号: IS_PROTOCOL (4)
协议版本: IS_PROTOCOL_VER (3)

STATUS消息格式:
+----------+-----------+---------+----------+----------+------------+
| Protocol | Version   | Command | MOM Port | RM Port  | Status...  |
| (int)    | (int)     | (int)   | (uint)   | (uint)   | (strings)  |
+----------+-----------+---------+----------+----------+------------+

Status字符串示例:
- "node=hostname"
- "arch=linux"
- "ncpus=16"
- "physmem=32gb"
- "availmem=28gb"
- "loadave=0.50"
- "state=free"
- "jobs=job1 job2"
```

---

## 6. 作业管理详细流程

### 6.1 作业生命周期

```
                        pbs_server                    pbs_mom
                            |                            |
    1. QueueJob    -------->|                            |
                            |-------- QueueJob --------->|
                            |                            | 创建作业结构
    2. JobCred     -------->|                            |
                            |-------- JobCred ---------->|
                            |                            | 存储凭证
    3. JobScript   -------->|                            |
                            |-------- JobScript -------->|
                            |                            | 保存脚本
    4. RdyToCommit -------->|                            |
                            |------ RdyToCommit -------->|
                            |                            | 准备执行
    5. Commit      -------->|                            |
                            |-------- Commit ----------->|
                            |                            | 开始执行
                            |                            |
                            |                            | [作业运行中]
                            |                            |
                            |<------- JobObit -----------|
                            |                            | 作业完成
```

### 6.2 作业执行流程

```c
// 作业执行的主要函数调用链
mom_req_quejob()           // 接收作业入队请求
  → req_jobcredential()    // 处理作业凭证
  → req_jobscript()        // 接收作业脚本
  → req_rdytocommit()      // 准备提交
  → req_commit()           // 提交执行
    → exec_job_on_ms()     // 在Mother Superior上执行
      → run_prologue_scripts()  // 运行prologue脚本
      → TMomFinalizeJob1()      // 准备执行环境
      → TMomFinalizeJob2()      // 创建cgroups等
      → TMomFinalizeJob3()      // fork执行进程
      → start_process()         // 启动作业进程
```

### 6.3 作业数据结构

```c
// 作业核心结构 (简化)
struct job {
    // 作业识别
    char            ji_qs.ji_jobid[PBS_MAXSVRJOBID+1];
    char            ji_qs.ji_fileprefix[PBS_JOBBASE+1];
    
    // 状态信息
    int             ji_qs.ji_state;      // JOB_STATE_*
    int             ji_qs.ji_substate;   // 子状态
    
    // 执行信息
    tm_node_id      ji_nodeid;           // 本节点ID
    int             ji_numnodes;         // 节点数
    struct hnodent *ji_hosts;            // 主机列表
    struct vnodent *ji_vnods;            // 虚拟节点
    
    // 任务管理
    tlist_head      ji_tasks;            // 任务列表
    int             ji_taskid;           // 下一个任务ID
    
    // 资源使用
    pbs_attribute   ji_wattr[JOB_ATR_LAST]; // 属性数组
    
    // Sister MOM信息
    hnodent        *ji_sisters;          // Sister列表
    int             ji_radix;            // Radix值
};
```

### 6.4 任务管理

```c
// 任务结构
struct task {
    struct task_qs {
        char        ti_parentjobid[PBS_MAXSVRJOBID+1];
        tm_node_id  ti_parentnode;       // 父节点
        tm_task_id  ti_task;             // 任务ID
        int         ti_status;           // TI_STATE_*
        int         ti_exitstat;         // 退出状态
        tm_event_t  ti_exitevent;        // 退出事件
    } ti_qs;
    
    pid_t           ti_sid;              // 会话ID
    int             ti_fd[2];            // 文件描述符
};

// 任务状态
#define TI_STATE_EMBRYO     0   // 初始状态
#define TI_STATE_RUNNING    1   // 运行中
#define TI_STATE_EXITED     2   // 已退出(正常)
#define TI_STATE_DEAD       3   // 已终止(异常)
```

---

## 7. 资源管理和分配

### 7.1 资源监控 (Resource Monitor)

pbs_mom持续监控以下系统资源：

| 资源名称 | 描述 | 采集方式 |
|----------|------|----------|
| `cput` | CPU时间 | /proc/[pid]/stat |
| `mem` | 内存使用(KB) | /proc/[pid]/statm |
| `resi` | 常驻内存(KB) | /proc/[pid]/statm |
| `vmem` | 虚拟内存(KB) | /proc/[pid]/status |
| `physmem` | 物理内存总量 | /proc/meminfo |
| `availmem` | 可用内存 | /proc/meminfo |
| `totmem` | 总内存 | /proc/meminfo |
| `ncpus` | CPU核心数 | /proc/cpuinfo |
| `loadave` | 系统负载 | /proc/loadavg |
| `netload` | 网络流量 | /proc/net/dev |
| `idletime` | 空闲时间 | /proc/stat |
| `walltime` | 墙钟时间 | 计算 |

### 7.2 状态报告数据

```c
// mom_server.c 中定义的状态属性
stat_record stats[] = {
    {"arch",        gen_arch},      // 架构
    {"opsys",       gen_gen},       // 操作系统
    {"uname",       gen_gen},       // 系统信息
    {"sessions",    gen_gen},       // 会话列表
    {"nsessions",   gen_gen},       // 会话数
    {"nusers",      gen_gen},       // 用户数
    {"idletime",    gen_gen},       // 空闲时间
    {"totmem",      gen_gen},       // 总内存
    {"availmem",    gen_gen},       // 可用内存
    {"physmem",     gen_gen},       // 物理内存
    {"ncpus",       gen_gen},       // CPU数
    {"loadave",     gen_gen},       // 负载
    {"message",     gen_gen},       // 消息
    {"gres",        gen_gres},      // 通用资源
    {"netload",     gen_gen},       // 网络负载
    {"size",        gen_size},      // 文件系统大小
    {"state",       gen_gen},       // 节点状态
    {"jobs",        gen_gen},       // 运行作业
    {"jobdata",     gen_jdata},     // 作业数据
    {"varattr",     gen_gen},       // 变量属性
    {"cpuclock",    gen_gen},       // CPU频率
    {"macaddr",     gen_macaddr},   // MAC地址
    {"layout",      gen_layout},    // 硬件布局(cgroups)
    {NULL,          NULL}
};
```

### 7.3 Cgroups资源隔离

当启用`PENABLE_LINUX_CGROUPS`时，pbs_mom使用Linux cgroups进行资源隔离：

```c
// Cgroup子系统
enum cgroup_system {
    cg_cpu,        // CPU限制
    cg_cpuset,     // CPU集合
    cg_cpuacct,    // CPU统计
    cg_memory,     // 内存限制
    cg_devices,    // 设备访问
    cg_subsys_count
};

// 为作业创建cgroup
// 路径: /sys/fs/cgroup/<subsys>/torque/<jobid>/

// 资源限制示例:
// - cpuset.cpus: 分配的CPU核心
// - cpuset.mems: 分配的NUMA节点
// - memory.limit_in_bytes: 内存限制
// - cpu.cfs_quota_us: CPU时间配额
```

### 7.4 NUMA支持

```c
// NUMA节点板结构
typedef struct nodeboard {
    int         num_cpus;        // CPU数量
    int         num_mems;        // 内存节点数
    hwloc_bitmap_t cpuset;       // CPU集合
    hwloc_bitmap_t memset;       // 内存集合
} nodeboard;

// 当启用NUMA_SUPPORT时
extern int            num_node_boards;
extern nodeboard      node_boards[MAX_NODE_BOARDS];
```

---

## 8. Server通信机制

### 8.1 通信模式

```
┌─────────────┐                              ┌─────────────┐
│ pbs_server  │                              │  pbs_mom    │
└─────────────┘                              └─────────────┘
       │                                            │
       │                                            │
       │  ┌──────── 启动阶段 ────────┐              │
       │  │                          │              │
       │<─┼──────── HELLO ──────────────────────────┤ MOM主动
       │  │                          │              │
       ├──┼──── CLUSTER_ADDRS ──────────────────────>│ Server响应
       │  │                          │              │
       │  └──────────────────────────┘              │
       │                                            │
       │  ┌──────── 运行阶段 ────────┐              │
       │  │                          │              │
       │<─┼────────── IS_STATUS ────────────────────┤ 周期性
       │  │                          │              │  (定时器)
       │<─┼────────── IS_UPDATE ────────────────────┤ 状态变更时
       │  │                          │              │
       │  │                          │              │
       ├──┼──── PBS_BATCH_* 请求 ───────────────────>│ 按需
       │  │                          │              │
       │<─┼──────── JobObit ────────────────────────┤ 作业完成
       │  │                          │              │
       │  └──────────────────────────┘              │
       │                                            │
```

### 8.2 谁主动调用谁

| 场景 | 发起方 | 接收方 | 说明 |
|------|--------|--------|------|
| **初始连接** | MOM | Server | MOM启动后主动发送HELLO |
| **集群地址** | Server | MOM | Server响应HELLO，发送集群信息 |
| **状态更新** | MOM | Server | MOM周期性发送STATUS |
| **状态变更** | MOM | Server | 节点状态变化时发送UPDATE |
| **作业请求** | Server | MOM | Server发起QueueJob/RunJob等 |
| **作业完成** | MOM | Server | MOM发送JobObit通知 |
| **文件传输** | Server | MOM | Server请求CopyFiles/ReturnFiles |
| **节点查询** | Server | MOM | Server发送StatusJob |

### 8.3 调用频率

```c
// 关键时间常量 (mom_server.c)
#define MAX_RETRY_TIME_IN_SECS           (5 * 60)    // 最大重试间隔
#define STARTING_RETRY_INTERVAL_IN_SECS   2          // 初始重试间隔
#define MIN_SERVER_UDPATE_SPACING         3          // 最小更新间隔
#define MAX_SERVER_UPDATE_SPACING         40         // 最大更新间隔
#define DEFAULT_SERVER_STAT_UPDATES       45         // 默认状态更新间隔

// 作业相关超时 (mom_main.c)
#define PMOMTCPTIMEOUT                    60         // TCP请求超时(秒)
#define DEFAULT_JOB_EXIT_WAIT_TIME        600        // 作业退出等待时间
#define MAX_JOIN_WAIT_TIME                600        // 最大加入等待时间
#define RESEND_WAIT_TIME                  300        // 重发等待时间

// OBIT重试参数
const int OBIT_BUSY_RETRY              = 6;          // OBIT忙重试次数
const int OBIT_RETRY_LIMIT             = 5;          // OBIT重试限制
const int ALREADY_EXITED_RETRY_TIME    = 30;         // 已退出重试时间
const int OBIT_SENT_WAIT_TIME          = 10;         // OBIT发送等待
const int MINUS_ONE_RETRY_TIME         = 15;         // -1重试时间
const int STAGE_WAIT_TIME              = 180;        // 暂存等待时间
```

### 8.4 状态更新触发条件

```c
// 状态更新触发 (mom_server.c)
void check_state(int force) {
    // 1. 周期性更新 (DEFAULT_SERVER_STAT_UPDATES秒)
    // 2. 强制更新 (force参数)
    // 3. 状态变更:
    //    - 节点状态变化
    //    - 作业启动/完成
    //    - 资源使用变化
    // 4. Server请求
}

// ReportMomState标志
// 每个server有独立的标志，状态变化时设置，UPDATE发送后清除
```

---

## 9. 主要功能模块

### 9.1 核心源文件

| 文件 | 功能 |
|------|------|
| `mom_main.c` | 主函数、初始化、主循环 |
| `mom_server.c` | Server通信、状态报告 |
| `mom_comm.c` | MOM间通信(Sister) |
| `mom_process_request.c` | 请求处理分发 |
| `mom_job_func.c` | 作业操作函数 |
| `start_exec.c` | 作业执行启动 |
| `catch_child.c` | 子进程结束处理 |
| `requests.c` | 批处理请求处理 |
| `mom_req_quejob.c` | 作业入队处理 |
| `checkpoint.c` | 检查点功能 |
| `prolog.c` | Prologue/Epilogue脚本 |
| `trq_cgroups.c` | Cgroups管理 |
| `linux/mom_mach.c` | Linux平台资源采集 |

### 9.2 请求处理函数映射

```c
// mom_process_request.c 中的处理函数
void mom_req_quejob(struct batch_request *preq);     // QueueJob
void req_jobcredential(struct batch_request *preq);  // JobCred
void req_jobscript(struct batch_request *preq);      // JobScript
void req_rdytocommit(struct batch_request *preq);    // RdyToCommit
void req_commit(struct batch_request *preq);         // Commit
void mom_req_holdjob(struct batch_request *preq);    // HoldJob
void req_deletejob(struct batch_request *preq);      // DeleteJob
void req_rerunjob(struct batch_request *preq);       // Rerun
void req_modifyjob(struct batch_request *preq);      // ModifyJob
void req_shutdown(struct batch_request *preq);       // Shutdown
void mom_req_signal_job(struct batch_request *preq); // SignalJob
void req_mvjobfile(struct batch_request *preq);      // MvJobFile
void req_checkpointjob(struct batch_request *preq);  // CheckpointJob
void req_messagejob(struct batch_request *preq);     // MessageJob
int  req_stat_job(struct batch_request *preq);       // StatusJob
```

---

## 10. 通信时序图

### 10.1 作业提交完整流程

```
用户        qsub          pbs_server        Mother_MOM        Sister_MOM
 │           │                │                  │                 │
 │──script──>│                │                  │                 │
 │           │──QueueJob─────>│                  │                 │
 │           │<──job_id──────│                  │                 │
 │<─job_id───│                │                  │                 │
 │           │                │                  │                 │
 │           │                │  [调度决策]       │                 │
 │           │                │                  │                 │
 │           │                │──QueueJob───────>│                 │
 │           │                │──JobCred────────>│                 │
 │           │                │──JobScript──────>│                 │
 │           │                │──RdyToCommit────>│                 │
 │           │                │<──ACK───────────│                 │
 │           │                │──Commit─────────>│                 │
 │           │                │                  │                 │
 │           │                │                  │──IM_JOIN_JOB───>│
 │           │                │                  │<──IM_ALL_OKAY──│
 │           │                │                  │                 │
 │           │                │                  │  [执行作业]      │
 │           │                │                  │                 │
 │           │                │                  │──IM_POLL_JOB──>│
 │           │                │                  │<──资源使用─────│
 │           │                │                  │                 │
 │           │                │<──IS_STATUS─────│                 │
 │           │                │                  │                 │
 │           │                │                  │<──IM_OBIT_TASK─│
 │           │                │                  │                 │
 │           │                │<──JobObit───────│                 │
 │           │                │──ACK───────────>│                 │
```

### 10.2 节点状态更新流程

```
pbs_mom                                    pbs_server
   │                                           │
   │  [定时器触发: DEFAULT_SERVER_STAT_UPDATES秒]
   │                                           │
   │──────────── IS_STATUS ───────────────────>│
   │    Protocol: IS_PROTOCOL (4)              │
   │    Version: IS_PROTOCOL_VER (3)           │
   │    Command: IS_STATUS (4)                 │
   │    MOM_Port: 15002                        │
   │    RM_Port: 15003                         │
   │    Status Strings:                        │
   │      - node=hostname                      │
   │      - state=free                         │
   │      - ncpus=16                           │
   │      - physmem=32gb                       │
   │      - availmem=28gb                      │
   │      - loadave=0.50                       │
   │      - jobs=job1 job2                     │
   │      - ...                                │
   │                                           │
   │                                    [更新节点表]
   │                                           │
```

---

## 11. 配置参数

### 11.1 主要配置选项

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `$pbsserver` | - | pbs_server地址 |
| `$logevent` | 0x1ff | 日志事件掩码 |
| `$loglevel` | 0 | 日志级别 |
| `$restricted` | - | 允许连接的主机 |
| `$cputmult` | 1.0 | CPU时间乘数 |
| `$wallmult` | 1.0 | 墙钟时间乘数 |
| `$usecp` | - | 本地复制路径映射 |
| `$rcpcmd` | scp | 远程复制命令 |
| `$ideal_load` | - | 理想负载值 |
| `$max_load` | - | 最大负载值 |
| `$job_output_file_mask` | 077 | 输出文件掩码 |

### 11.2 环境变量

| 变量 | 说明 |
|------|------|
| `PBS_HOME` | TORQUE安装目录 |
| `PBS_SERVER` | 默认服务器 |
| `PBS_DEFAULT` | 默认服务器(旧) |
| `PBS_MOM_HOME` | MOM工作目录 |

---

## 12. 错误处理

### 12.1 主要错误码

```c
// pbs_error.h 中定义
#define PBSE_NONE         0     // 无错误
#define PBSE_SYSTEM      15001  // 系统错误
#define PBSE_INTERNAL    15002  // 内部错误
#define PBSE_BADHOST     15007  // 无效主机
#define PBSE_NOCONNECTS  15020  // 无可用连接
#define PBSE_PROTOCOL    15025  // 协议错误
#define PBSE_JOBEXIST    15035  // 作业已存在
#define PBSE_UNKJOBID    15001  // 未知作业ID
#define PBSE_NOATTR      15047  // 未知属性
#define PBSE_TIMEOUT     15053  // 超时
```

### 12.2 重试机制

```c
// 连接重试 (指数退避)
retry_interval = STARTING_RETRY_INTERVAL_IN_SECS;  // 2秒
max_retry = MAX_RETRY_TIME_IN_SECS;                // 5分钟

while (retry_interval < max_retry) {
    if (connect_success)
        break;
    sleep(retry_interval);
    retry_interval *= 2;
}
```
