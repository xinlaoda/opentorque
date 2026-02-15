# TORQUE 项目分析报告

## 1. 项目概述

**TORQUE** (Terascale Open-source Resource and Queue Manager) 是一个基于PBS (Portable Batch System) 的开源分布式资源管理和作业队列系统，由NASA、LLNL和MRJ原始开发，后经多个组织持续增强。

---

## 2. 核心服务组件

### 2.1 pbs_server - 批处理服务器

**功能描述**：
- 中央作业管理服务器，负责接收、调度和管理批处理作业
- 维护作业队列、节点状态和系统配置
- 与调度器和MOM守护进程协调工作

**默认端口**: 15001

**主要功能**:
- 作业队列管理（创建、删除、配置队列）
- 作业生命周期管理（提交、运行、挂起、删除）
- 节点资源跟踪和分配
- 用户认证和访问控制
- 日志记录和会计审计

**启动命令**:
```bash
pbs_server [-a active] [-c] [-d config_path] [-e] [-F] [-f] [-h] 
           [-l location] [-p port] [-t type] [-v] [-A acctfile] 
           [-D] [-H hostname] [-L logfile] [-M mom_port] 
           [-n] [-R momRPP_port] [-S scheduler_port]
           [--about] [--ha] [--help] [--version]
```

---

### 2.2 pbs_mom - 节点执行守护进程

**功能描述**：
- Machine Oriented Mini-server，运行在每个计算节点上
- 负责本地作业执行、资源监控和状态报告
- 管理作业的实际执行环境

**默认端口**: 15002 (服务端口), 15003 (管理端口)

**主要功能**:
- 作业脚本执行
- 进程监控和资源使用跟踪
- Checkpoint/Restart支持
- 与pbs_server通信报告状态
- Cgroups资源隔离（可选）
- GPU/MIC加速器管理

---

### 2.3 pbs_sched - 作业调度器

**功能描述**：
- 负责决定哪些作业应该运行以及在哪些节点上运行
- 支持多种调度策略实现

**默认端口**: 15004

**调度器类型**:
| 类型 | 描述 |
|------|------|
| pbs_sched_fifo | FIFO先进先出调度 |
| pbs_sched_basl | BASL脚本调度器 |
| pbs_sched_tcl | TCL脚本调度器 |
| pbs_sched_cc | C++调度器（含多种集群样例） |

---

### 2.4 trqauthd - 认证守护进程

**功能描述**：
- TORQUE 4.0引入的认证服务，替代原有的pbs_iff
- 处理客户端命令的用户认证请求
- 多线程设计，支持高并发认证

**默认端口**: 15005 (本地回环)

**特点**:
- 必须以root运行
- 驻留服务，无需每次调用
- 通过Unix域套接字与客户端通信

---

## 3. 命令行工具

### 3.1 作业管理命令

| 命令 | 功能描述 |
|------|----------|
| **qsub** | 提交作业到批处理队列 |
| **qstat** | 查询作业、队列或服务器状态 |
| **qdel** | 删除/终止批处理作业 |
| **qalter** | 修改已提交作业的属性 |
| **qhold** | 挂起作业 |
| **qrls** | 释放被挂起的作业 |
| **qrun** | 强制运行作业 |
| **qrerun** | 重新运行作业 |
| **qmove** | 移动作业到其他队列 |
| **qorder** | 调整作业顺序 |
| **qsig** | 向作业发送信号 |
| **qmsg** | 向作业发送消息 |
| **qchkpt** | 对作业进行检查点操作 |
| **qselect** | 选择符合条件的作业 |

### 3.2 队列/服务器管理命令

| 命令 | 功能描述 |
|------|----------|
| **qmgr** | 批处理系统管理器（创建/删除/配置队列和服务器） |
| **qstart** | 启动队列处理 |
| **qstop** | 停止队列处理 |
| **qenable** | 启用队列接受作业 |
| **qdisable** | 禁用队列接受作业 |
| **qterm** | 终止批处理服务器 |

### 3.3 节点管理命令

| 命令 | 功能描述 |
|------|----------|
| **pbsnodes** | 管理和查询计算节点状态 |
| **momctl** | MOM控制工具 |

### 3.4 其他工具

| 命令 | 功能描述 |
|------|----------|
| **pbsdsh** | 分布式Shell，在作业分配的节点上执行命令 |
| **pbs_track** | 跟踪作业执行 |
| **nqs2pbs** | NQS到PBS转换工具 |

---

## 4. API接口定义

### 4.1 C语言API (libpbs)

#### 连接管理

```c
// 连接到PBS服务器
int pbs_connect(char *server);

// 断开连接
int pbs_disconnect(int connect);

// 获取默认服务器
char *pbs_default(void);

// 获取服务器列表
char *pbs_get_server_list(void);

// 获取错误消息
char *pbs_geterrmsg(int connect);
```

#### 作业操作

```c
// 提交作业
char *pbs_submit(int connect, struct attropl *attrib, 
                 char *script, char *destination, char *extend);

// 删除作业
int pbs_deljob(int connect, char *job_id, char *extend);

// 修改作业属性
int pbs_alterjob(int connect, char *job_id, 
                 struct attrl *attrib, char *extend);

// 异步修改作业
int pbs_alterjob_async(int connect, char *job_id, 
                       struct attrl *attrib, char *extend);

// 挂起作业
int pbs_holdjob(int connect, char *job_id, 
                char *hold_type, char *extend);

// 释放作业
int pbs_rlsjob(int connect, char *job_id, 
               char *hold_type, char *extend);

// 运行作业
int pbs_runjob(int connect, char *jobid, char *loc, char *extend);

// 异步运行作业
int pbs_asyrunjob(int c, char *jobid, char *location, char *extend);

// 重新运行作业
int pbs_rerunjob(int connect, char *job_id, char *extend);

// 移动作业
int pbs_movejob(int connect, char *job_id, 
                char *destination, char *extend);

// 发送信号到作业
int pbs_sigjob(int connect, char *job_id, 
               char *signal, char *extend);

// 异步发送信号
int pbs_sigjobasync(int connect, char *job_id, 
                    char *signal, char *extend);

// 发送消息到作业
int pbs_msgjob(int connect, char *job_id, int file, 
               char *message, char *extend);

// 检查点作业
int pbs_checkpointjob(int connect, char *job_id, char *extend);

// 调整作业顺序
int pbs_orderjob(int connect, char *job1, char *job2, char *extend);

// 定位作业
char *pbs_locjob(int connect, char *job_id, char *extend);

// 选择作业
char **pbs_selectjob(int connect, struct attropl *select_list, 
                     char *extend);
```

#### 状态查询

```c
// 查询作业状态
struct batch_status *pbs_statjob(int connect, char *id, 
                                  struct attrl *attrib, char *extend);

// 选择性查询作业
struct batch_status *pbs_selstat(int connect, 
                                  struct attropl *select_list, 
                                  char *extend);

// 查询队列状态
struct batch_status *pbs_statque(int connect, char *id, 
                                  struct attrl *attrib, char *extend);

// 查询服务器状态
struct batch_status *pbs_statserver(int connect, 
                                     struct attrl *attrib, char *extend);

// 查询节点状态
struct batch_status *pbs_statnode(int connect, char *id, 
                                   struct attrl *attrib, char *extend);

// 释放状态结构
void pbs_statfree(struct batch_status *stat);
```

#### 服务器管理

```c
// 管理操作（创建/删除/设置队列、节点等）
int pbs_manager(int connect, int command, int obj_type, 
                char *obj_name, struct attropl *attrib, char *extend);

// 终止服务器
int pbs_terminate(int connect, int manner, char *extend);
```

#### 资源管理

```c
// 资源查询
int pbs_rescquery(int connect, char **rlist, int nresc, 
                  int *avail, int *alloc, int *resv, int *down);

// 资源预留
int pbs_rescreserve(int connect, char **rlist, int nresc, 
                    resource_t *phandle);

// 资源释放
int pbs_rescrelease(int connect, resource_t rhandle);

// 资源可用性查询
char *avail(int connect, char *resc);
```

#### GPU管理

```c
// GPU模式控制
int pbs_gpumode(int connect, char *node, char *gpuid, int gpumode);
```

### 4.2 核心数据结构

```c
// 属性列表结构
struct attrl {
    struct attrl  *next;
    char          *name;
    char          *resource;
    char          *value;
    enum batch_op  op;
};

// 带操作的属性列表
struct attropl {
    struct attropl *next;
    char   *name;
    char   *resource;
    char   *value;
    enum batch_op   op;
};

// 批处理状态结构
struct batch_status {
    struct batch_status *next;
    char                *name;
    struct attrl        *attribs;
    char                *text;
};

// 批处理操作类型
enum batch_op { 
    SET, UNSET, INCR, DECR, 
    EQ, NE, GE, GT, LE, LT, 
    DFLT, MERGE, INCR_OLD, SET_PLUGIN 
};
```

### 4.3 DRMAA API

TORQUE提供符合DRMAA 1.0规范的标准API：

```c
// 初始化/结束
int drmaa_init(const char *contact, char *error_diagnosis, 
               size_t error_diag_len);
int drmaa_exit(char *error_diagnosis, size_t error_diag_len);

// 作业模板
int drmaa_allocate_job_template(drmaa_job_template_t **jt, 
                                char *error_diagnosis, 
                                size_t error_diag_len);
int drmaa_delete_job_template(drmaa_job_template_t *jt, 
                              char *error_diagnosis, 
                              size_t error_diag_len);
int drmaa_set_attribute(drmaa_job_template_t *jt, 
                        const char *name, const char *value, 
                        char *error_diagnosis, size_t error_diag_len);

// 作业提交
int drmaa_run_job(char *job_id, size_t job_id_len, 
                  drmaa_job_template_t *jt, 
                  char *error_diagnosis, size_t error_diag_len);
int drmaa_run_bulk_jobs(drmaa_job_ids_t **jobids, 
                        drmaa_job_template_t *jt, 
                        int start, int end, int incr, 
                        char *error_diagnosis, size_t error_diag_len);

// 作业控制
int drmaa_control(const char *jobid, int action, 
                  char *error_diagnosis, size_t error_diag_len);

// 同步等待
int drmaa_synchronize(const char **job_ids, 
                      signed long timeout, int dispose, 
                      char *error_diagnosis, size_t error_diag_len);
int drmaa_wait(const char *job_id, char *job_id_out, 
               size_t job_id_out_len, int *stat, 
               signed long timeout, drmaa_attr_values_t **rusage, 
               char *error_diagnosis, size_t error_diag_len);

// 状态查询
int drmaa_job_ps(const char *job_id, int *remote_ps, 
                 char *error_diagnosis, size_t error_diag_len);
```

---

## 5. 端口配置汇总

| 服务 | 默认端口 | 说明 |
|------|----------|------|
| pbs_server | 15001 | 批处理服务器主端口 |
| pbs_mom | 15002 | MOM服务端口 |
| pbs_mom (管理) | 15003 | MOM管理端口 |
| pbs_sched | 15004 | 调度器端口 |
| trqauthd | 15005 | 认证守护进程端口 |

---

## 6. 主要作业属性

| 属性名 | 宏定义 | 描述 |
|--------|--------|------|
| Execution_Time | ATTR_a | 作业执行时间 |
| Account_Name | ATTR_A | 账户名称 |
| Checkpoint | ATTR_c | 检查点选项 |
| Error_Path | ATTR_e | 错误输出路径 |
| Hold_Types | ATTR_h | 挂起类型 |
| Join_Path | ATTR_j | 合并输出路径 |
| Keep_Files | ATTR_k | 保留文件选项 |
| Resource_List | ATTR_l | 资源需求列表 |
| Mail_Points | ATTR_m | 邮件通知点 |
| Mail_Users | ATTR_M | 邮件接收用户 |
| Job_Name | ATTR_N | 作业名称 |
| Output_Path | ATTR_o | 标准输出路径 |
| Priority | ATTR_p | 优先级 |
| destination | ATTR_q | 目标队列 |
| Rerunable | ATTR_r | 可重运行标志 |
| job_array_request | ATTR_t | 作业数组请求 |
| User_List | ATTR_u | 用户列表 |
| Variable_List | ATTR_v | 环境变量列表 |
| Shell_Path_List | ATTR_S | Shell路径 |
| depend | ATTR_depend | 作业依赖 |
| stagein | ATTR_stagein | 输入文件暂存 |
| stageout | ATTR_stageout | 输出文件暂存 |

---

## 7. 架构图

```
┌─────────────────────────────────────────────────────────────────┐
│                         用户/管理员                              │
└─────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────┐
│                    命令行工具 (qsub, qstat, qmgr, etc.)         │
└─────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────┐
│                    libpbs API / DRMAA API                       │
└─────────────────────────────────────────────────────────────────┘
                                │
            ┌───────────────────┼───────────────────┐
            ▼                   ▼                   ▼
┌───────────────────┐  ┌───────────────────┐  ┌───────────────────┐
│    trqauthd       │  │    pbs_server     │  │    pbs_sched      │
│  (认证:15005)     │  │  (服务器:15001)   │  │   (调度:15004)    │
└───────────────────┘  └───────────────────┘  └───────────────────┘
                                │
                ┌───────────────┼───────────────┐
                ▼               ▼               ▼
        ┌───────────┐   ┌───────────┐   ┌───────────┐
        │ pbs_mom   │   │ pbs_mom   │   │ pbs_mom   │
        │ (节点1)   │   │ (节点2)   │   │ (节点N)   │
        │ :15002    │   │ :15002    │   │ :15002    │
        └───────────┘   └───────────┘   └───────────┘
              │               │               │
              ▼               ▼               ▼
        ┌───────────┐   ┌───────────┐   ┌───────────┐
        │  作业进程  │   │  作业进程  │   │  作业进程  │
        └───────────┘   └───────────┘   └───────────┘
```

---

## 8. 可选功能模块

- **BLCR支持**: Berkeley Lab Checkpoint/Restart
- **CPUset支持**: CPU集合隔离
- **Cgroups支持**: 控制组资源管理
- **NUMA支持**: 非统一内存访问优化
- **PAM集成**: 可插拔认证模块
- **Munge认证**: 消息认证
- **GPU管理**: NVIDIA GPU和Intel MIC支持
- **ALPS集成**: Cray系统ALPS集成
