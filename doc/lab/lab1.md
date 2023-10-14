# Lab1—MapReduce设计实现
在设计实现Lab1时，需要阅读并深刻理解MapReduce论文的第三章，根据其设计理念实现即可。
## Coordinator/Master设计实现
Coordinator是MapReduce框架的大脑，负责任务的记录、调度等。因此在Coordinator中需要保存一些关键的数据结构，例如Map任务、Reduce任务；对于每一个任务，还需要保存任务的状态（空闲、进行中、已完成等）。

考虑到Lab1使用线程模拟Worker，情况会比真实的MapReduce简单一些，因此设计时会有一些简化，主要如下：
1. Coordinator不记录Map任务输出的中间结果的位置。

如果是实际的MapReduce框架，由于Coordinator和Worker都是位于不同的机器上运行，因此每一个Map任务执行完成后生成的中间结果的位置也需要由执行Map任务的Worker发送给Coordinator，并由Coordinator记录。在Reduce阶段，如果由于该Map Worker发生了故障，执行Reduce任务的Worker无法从该Map Worker所属的机器读取文件时，可以报告给Coordinator，并由Coordinator重新分配该Map任务，以使MapReduce正常进行。Lab1中，Map任务与Reduce任务运行在同一台机器，中间结果也记录在本地磁盘，不会发生上述情况。
2. 不区分Map Task和Reduce Task

实际的MapReduce框架中，Reduce Worker工作时可能访问不到Map Task生成的中间结果，因此在Reduce执行阶段，可能会让Map Task重新执行。但在Lab1中，Map Task生成的中间结果一定可以被Reduce Task访问到，因此可以不必同时记录Map Task和Reduce Task，只记录当前的Tasks。如果是Map阶段，那么当前Tasks一定都是Map Task；同理，如果当前位于Reduce阶段，那么所有的Task都是Reduce Task。
   
基于以上考虑，Coordinator的数据结构设计如下：
```Go
type Coordinator struct {
    tasks   []Task // tasks need to be assigned and executed
    nReduce int // number of reduce tasks; it is the same as the number of final files
    nMap    int // number of map tasks
    status  CoordinatorStatus // the phase of MapReduce, it can be Map, Reduce or Finish
    mu      sync.Mutex // the lock used for mutually exclusive accessing
}
```
其中Task结构体既可以表示Map Task，也可以表示Reduce Task，其结构体如下：
```Go
type Task struct {
    tno       int // task id
    filenames []string // files need to be processed by this task
    status    TaskStatus // task status, it can be idle, in_progess or completed
    startTime time.Time // start time of the task， mainly used to reassign the task when the task is overtime
}
```
由于Coordinator与Worker是一对多的关系，因此在被Worker调用的方法中，需要加锁保护共享资源，避免共享资源被多个线程同时访问并修改。

## Worker设计实现
Worker的逻辑相对简单一些，轮训做任务即可，其主要逻辑如下，分别实现doMapTask和doReduceTask即可。
```Go
func Worker(mapf func(string, string) []KeyValue,
    reducef func(string, []string) string) {
    for {
       args := GetTaskArgs{}
       reply := GetTaskReply{}
       ok := CallGetTask(&args, &reply)

       // handle map fynction
       switch reply.Type {
       case MAP:
          if len(reply.Filenames) < 1 {
             log.Fatalf("don't have filename")
          }
          doMapTask(reply.Filenames[0], reply.Task_no, reply.NReduce, mapf)
       case REDUCE:
          if len(reply.Filenames) < 1 {
             log.Fatalf("don't have filenames")
          }
          doReduceTask(reply.Filenames, reply.Task_no, reducef)
       case WAIT:
          log.Printf("wait task\n")
          time.Sleep(time.Second)
       default:
          time.Sleep(time.Second)
       }
    }
}
```