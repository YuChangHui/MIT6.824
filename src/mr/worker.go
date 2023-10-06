package mr

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"sort"
	"strings"
	"time"
)
import "log"
import "net/rpc"
import "hash/fnv"

// Map functions return a slice of KeyValue.
type KeyValue struct {
	Key   string
	Value string
}

// for sorting by key.
type ByKey []KeyValue

// for sorting by key.
func (a ByKey) Len() int           { return len(a) }
func (a ByKey) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ByKey) Less(i, j int) bool { return a[i].Key < a[j].Key }

// use ihash(key) % NReduce to choose the reduce
// task number for each KeyValue emitted by Map.
func ihash(key string) int {
	h := fnv.New32a()
	h.Write([]byte(key))
	return int(h.Sum32() & 0x7fffffff)
}

// main/mrworker.go calls this function.
func Worker(mapf func(string, string) []KeyValue,
	reducef func(string, []string) string) {

	// Your worker implementation here.
	for {
		args := GetTaskArgs{}
		reply := GetTaskReply{}
		log.Printf("get task request: %v\n", args)
		ok := CallGetTask(&args, &reply)
		log.Printf("recv get task reply: %v\n", reply)
		if !ok || reply.Type == STOP {
			break
		}

		switch reply.Type {
		case MAP:
			if len(reply.Filenames) < 1 {
				log.Fatal("don't have filename in map task")
			}
			DoMap(reply.Filenames[0], reply.Task_no, reply.NReduce, mapf)
			// map task completed, send msg to master
			finish_args := FinishTaskArgs{
				Type:    MAP,
				Task_no: reply.Task_no,
			}
			finish_reply := FinishTaskReply{}
			log.Printf("finish request: %v\n", finish_args)
			CallFinishTask(&finish_args, &finish_reply)
			log.Printf("recv finish reply: %v\n", finish_reply)
		case REDUCE:
			if len(reply.Filenames) < 1 {
				log.Fatal("don't have filenames in reduce task")
			}
			DoReduce(reply.Filenames, reply.Task_no, reducef)
			// reduce task complete, send msg to master
			finish_args := FinishTaskArgs{
				Type:    REDUCE,
				Task_no: reply.Task_no,
			}
			finish_reply := FinishTaskReply{}
			log.Printf("finish request: %v\n", finish_args)
			CallFinishTask(&finish_args, &finish_reply)
			log.Printf("recv finish reply: %v\n", finish_reply)
		case WAIT:
			log.Println("wait task")
			time.Sleep(time.Second)
		default:
			time.Sleep(time.Second)
		}
	}

	// uncomment to send the Example RPC to the coordinator.
	// CallExample()

}

func DoMap(filename string, task_no int, nReduce int, mapf func(string, string) []KeyValue) {
	file, err := os.Open(filename)
	if err != nil {
		log.Fatalf("cannot open %v\n", filename)
	}
	content, err := ioutil.ReadAll(file)
	if err != nil {
		log.Fatalf("cannot read %v\n", filename)
	}
	file.Close()

	kva := mapf(filename, string(content))

	sort.Sort(ByKey(kva))

	log.Println("encode to json")
	files := make([]*os.File, nReduce)
	encoders := make([]*json.Encoder, nReduce)
	for i := 0; i < nReduce; i++ {
		ofile, inner_err := ioutil.TempFile("", "mr-tmp*")
		if inner_err != nil {
			log.Fatalf("cannot create temp file\n")
		}
		defer ofile.Close()

		encoder := json.NewEncoder(ofile)
		encoders[i] = encoder
		files[i] = ofile
	}

	var index int
	for _, kv := range kva {
		index = ihash(kv.Key) % nReduce
		inner_err := encoders[index].Encode(&kv)
		if inner_err != nil {
			log.Fatalf("cannot encode %v\n", kv)
		}
	}

	for i := 0; i < nReduce; i++ {
		filename_tmp := fmt.Sprintf("mr-%d-%d", task_no, i)
		inner_err := Move(files[i].Name(), filename_tmp)
		if inner_err != nil {
			log.Fatalf("cannot rename %v to %v in DoMap, err is %v\n", files[i].Name(), filename_tmp, inner_err)
		}
	}
}

func DoReduce(filenames []string, task_no int, reducef func(string, []string) string) {
	kva := make([]KeyValue, 0)
	for _, filename := range filenames {
		file, err := os.Open(filename)
		if err != nil {
			log.Fatalf("cannot open %v in DoReduce\n", filename)
		}
		defer file.Close()

		dec := json.NewDecoder(file)
		for {
			var kv KeyValue
			if err := dec.Decode(&kv); err != nil {
				break
			}
			kva = append(kva, kv)
		}
	}

	sort.Sort(ByKey(kva))

	ofile, err := ioutil.TempFile("", "mr-out-tmp*")
	if err != nil {
		log.Fatalf("cannot create temp file in DoReduce\n")
	}
	defer ofile.Close()

	i := 0
	for i < len(kva) {
		j := i + 1
		for j < len(kva) && kva[j].Key == kva[i].Key {
			j++
		}
		values := []string{}
		for k := i; k < j; k++ {
			values = append(values, kva[k].Value)
		}
		output := reducef(kva[i].Key, values)

		// this is the correct format for each line of Reduce output.
		fmt.Fprintf(ofile, "%v %v\n", kva[i].Key, output)

		i = j
	}

	output_filename := fmt.Sprintf("mr-out-%d", task_no)
	err = Move(ofile.Name(), output_filename)
	if err != nil {
		log.Fatalf("cannot rename %v to %v in DoReduce\n", ofile.Name(), output_filename)
	}
}

func CallGetTask(args *GetTaskArgs, reply *GetTaskReply) bool {
	return call("Coordinator.GetTask", args, reply)
}
func CallFinishTask(args *FinishTaskArgs, reply *FinishTaskReply) bool {
	return call("Coordinator.FinishTask", args, reply)
}

func Move(source, destination string) error {
	err := os.Rename(source, destination)
	if err != nil && strings.Contains(err.Error(), "invalid cross-device link") {
		return moveCrossDevice(source, destination)
	}
	return err
}

func moveCrossDevice(source, destination string) error {
	src, err := os.Open(source)
	if err != nil {
		return err
	}
	dst, err := os.Create(destination)
	if err != nil {
		src.Close()
		return err
	}
	_, err = io.Copy(dst, src)
	src.Close()
	dst.Close()
	if err != nil {
		return err
	}
	fi, err := os.Stat(source)
	if err != nil {
		os.Remove(destination)
		return err
	}
	err = os.Chmod(destination, fi.Mode())
	if err != nil {
		os.Remove(destination)
		return err
	}
	os.Remove(source)
	return nil
}

// example function to show how to make an RPC call to the coordinator.
//
// the RPC argument and reply types are defined in rpc.go.
func CallExample() {

	// declare an argument structure.
	args := ExampleArgs{}

	// fill in the argument(s).
	args.X = 99

	// declare a reply structure.
	reply := ExampleReply{}

	// send the RPC request, wait for the reply.
	call("Coordinator.Example", &args, &reply)

	// reply.Y should be 100.
	fmt.Printf("reply.Y %v\n", reply.Y)
}

// send an RPC request to the coordinator, wait for the response.
// usually returns true.
// returns false if something goes wrong.
func call(rpcname string, args interface{}, reply interface{}) bool {
	// c, err := rpc.DialHTTP("tcp", "127.0.0.1"+":1234")
	sockname := coordinatorSock()
	c, err := rpc.DialHTTP("unix", sockname)
	if err != nil {
		log.Fatal("dialing:", err)
	}
	defer c.Close()

	err = c.Call(rpcname, args, reply)
	if err == nil {
		return true
	}

	fmt.Println(err)
	return false
}
