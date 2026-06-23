package rpc

import (
	"time"

	"google.golang.org/grpc"

	"github.com/r0n9/nodekeep/model"
	pb "github.com/r0n9/nodekeep/proto"
	"github.com/r0n9/nodekeep/service/dao"
	rpcService "github.com/r0n9/nodekeep/service/rpc"
)

func ServeRPC() *grpc.Server {
	server := grpc.NewServer()
	pb.RegisterProbeServiceServer(server, &rpcService.ProbeHandler{
		Auth: &rpcService.AuthHandler{},
	})
	return server
}

func DispatchTask(duration time.Duration) {
	var index uint64 = 0
	for {
		var tasks []model.Monitor
		dao.DB.Find(&tasks)
		targets := dao.SortedTaskTargetsSnapshot()
		startedAt := time.Now()
		if len(targets) > 0 {
			for i := 0; i < len(tasks); i++ {
				if index >= uint64(len(targets)) {
					index = 0
				}
				target := targets[index]
				target.Send(tasks[i].PB())
				index++
			}
		} else {
			if index > 0 {
				index = 0
			}
		}
		time.Sleep(time.Until(startedAt.Add(duration)))
	}
}
