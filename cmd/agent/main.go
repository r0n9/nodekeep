package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/blang/semver"
	"github.com/genkiroid/cert"
	"github.com/go-ping/ping"
	"github.com/rhysd/go-github-selfupdate/selfupdate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/r0n9/nodekeep/cmd/agent/monitor"
	"github.com/r0n9/nodekeep/model"
	"github.com/r0n9/nodekeep/pkg/utils"
	pb "github.com/r0n9/nodekeep/proto"
	"github.com/r0n9/nodekeep/service/dao"
	"github.com/r0n9/nodekeep/service/rpc"
)

var (
	server       string
	clientSecret string
	nicAllowlist string
	version      = "develop"
)

var (
	delayWhenError = time.Second * 10       // Agent 重连间隔
	updateCh       = make(chan struct{}, 0) // Agent 自动更新间隔
	httpClient     = &http.Client{
		Timeout: time.Second * 30,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
)

func doSelfUpdate() {
	defer func() {
		time.Sleep(time.Minute * 30)
		updateCh <- struct{}{}
	}()
	v, err := parseAgentVersion()
	if err != nil {
		log.Println("Skip binary update, invalid version:", version)
		return
	}
	log.Println("Check update", v)
	latest, err := selfupdate.UpdateSelf(v, "r0n9/nodekeep")
	if err != nil {
		log.Println("Binary update failed:", err)
		return
	}
	if latest.Version.Equals(v) {
		// latest version is the same as current version. It means current binary is up to date.
		log.Println("Current binary is the latest version", version)
	} else {
		log.Println("Successfully updated to version", latest.Version)
		os.Exit(1)
	}
}

func parseAgentVersion() (semver.Version, error) {
	return semver.Parse(strings.TrimPrefix(version, "v"))
}

func shouldSelfUpdate() bool {
	_, err := parseAgentVersion()
	return err == nil
}

func init() {
	cert.TimeoutSeconds = 30
}

func main() {
	// 来自于 GoReleaser 的版本号
	dao.Version = version

	var debug bool
	flag.String("i", "", "unused 旧Agent配置兼容")
	flag.BoolVar(&debug, "d", false, "允许不安全连接")
	flag.StringVar(&server, "s", "localhost:8008", "管理面板地址")
	flag.StringVar(&clientSecret, "p", "", "Agent连接Secret")
	flag.StringVar(&nicAllowlist, "n", "", "网卡白名单，多个网卡用英文逗号分隔；为空时自动过滤虚拟网卡")
	flag.Parse()
	monitor.SetNICAllowlist(nicAllowlist)

	dao.Conf = &model.Config{
		Debug: debug,
	}

	if server == "" || clientSecret == "" {
		flag.Usage()
		return
	}

	run()
}

func run() {
	auth := rpc.AuthHandler{
		ClientSecret: clientSecret,
	}

	// 更新IP信息
	monitor.RefreshIP()
	go monitor.UpdateIP()

	if shouldSelfUpdate() {
		go func() {
			for range updateCh {
				go doSelfUpdate()
			}
		}()
		updateCh <- struct{}{}
	}

	var err error
	var conn *grpc.ClientConn

	retry := func() {
		log.Println("Error to close connection ...")
		if conn != nil {
			conn.Close()
		}
		time.Sleep(delayWhenError)
		log.Println("Try to reconnect ...")
	}

	for {
		conn, err = grpc.Dial(server, grpcDialOptions(&auth)...)
		if err != nil {
			log.Printf("grpc.Dial err: %v", err)
			retry()
			continue
		}
		err = runClient(pb.NewProbeServiceClient(conn))
		if err != nil {
			log.Printf("agent connection exited: %v", err)
			retry()
			continue
		}
		retry()
	}
}

func runClient(client pb.ProbeServiceClient) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 第一步注册
	if err := reportSystemInfo(ctx, client); err != nil {
		return fmt.Errorf("ReportSystemInfo: %w", err)
	}

	tasks, err := client.RequestTask(ctx, monitor.GetHost().PB())
	if err != nil {
		return fmt.Errorf("RequestTask: %w", err)
	}

	errCh := make(chan error, 2)
	go func() {
		errCh <- reportState(ctx, client)
	}()
	go func() {
		errCh <- receiveTasks(client, tasks)
	}()

	err = <-errCh
	cancel()
	return err
}

func grpcDialOptions(auth *rpc.AuthHandler) []grpc.DialOption {
	options := []grpc.DialOption{
		grpc.WithPerRPCCredentials(auth),
	}
	if dao.Conf.Debug {
		options = append(options, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		options = append(options, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{})))
	}
	return options
}

func receiveTasks(client pb.ProbeServiceClient, tasks pb.ProbeService_RequestTaskClient) error {
	var err error
	defer log.Printf("receiveTasks exit %v => %v", time.Now(), err)
	for {
		var task *pb.Task
		task, err = tasks.Recv()
		if err != nil {
			return err
		}
		go doTask(client, task)
	}
}

func doTask(client pb.ProbeServiceClient, task *pb.Task) {
	var result pb.TaskResult
	result.Id = task.GetId()
	result.Type = task.GetType()
	taskData := strings.TrimSpace(task.GetData())
	switch task.GetType() {
	case model.TaskTypeHTTPGET:
		start := time.Now()
		resp, reqErr := httpClient.Get(taskData)
		var statusErr error
		if reqErr == nil {
			defer resp.Body.Close()
			result.Delay = float32(time.Now().Sub(start).Microseconds()) / 1000.0
			if resp.StatusCode > 399 || resp.StatusCode < 200 {
				statusErr = errors.New("\n应用错误：" + resp.Status)
			}
		}
		// 证书检查与 HTTP 状态解耦：站点返回 4xx/5xx 时请求本身是成功的，
		// 证书仍要照常检查，否则站点异常会掩盖证书到期。
		// 传输层失败（连不上、超时）时不查证书：那种情况证书同样取不到，
		// 报「SSL证书错误」只会制造噪音。
		var certData string
		if reqErr == nil {
			certData = fetchCertData(taskData)
		}
		result.Data, result.Successful = httpTaskOutcome(reqErr, statusErr, certData)
	case model.TaskTypeICMPPing:
		pinger, err := ping.NewPinger(taskData)
		if err == nil {
			pinger.SetPrivileged(true)
			pinger.Count = 10
			pinger.Timeout = time.Second * 20
			err = pinger.Run() // Blocks until finished.
		}
		if err == nil {
			result.Delay = float32(pinger.Statistics().AvgRtt.Microseconds()) / 1000.0
			result.Successful = true
		} else {
			result.Data = err.Error()
		}
	case model.TaskTypeTCPPing:
		start := time.Now()
		conn, err := net.DialTimeout("tcp", taskData, time.Second*10)
		if err == nil {
			_ = conn.SetDeadline(time.Now().Add(time.Second * 10))
			if _, err = conn.Write([]byte("ping\n")); err == nil {
				result.Delay = float32(time.Now().Sub(start).Microseconds()) / 1000.0
				result.Successful = true
			}
			if closeErr := conn.Close(); err == nil && closeErr != nil {
				err = closeErr
			}
			if err != nil {
				result.Data = err.Error()
			}
		} else {
			result.Data = err.Error()
		}
	case model.TaskTypeCommand:
		startedAt := time.Now()
		var cmd *exec.Cmd
		pg, err := utils.NewProcessExitGroup()
		if err != nil {
			// 进程组创建失败，直接退出
			result.Data = err.Error()
			reportTaskResult(client, &result)
			return
		}
		cmdCtx, cancel := context.WithTimeout(context.Background(), time.Hour*2)
		defer cancel()
		if utils.IsWindows() {
			cmd = exec.CommandContext(cmdCtx, "cmd", "/c", task.GetData())
		} else {
			cmd = exec.CommandContext(cmdCtx, "sh", "-c", task.GetData())
		}
		if err := pg.AddProcess(cmd); err != nil {
			result.Data = err.Error()
			reportTaskResult(client, &result)
			return
		}
		cmd.Cancel = pg.Dispose
		cmd.WaitDelay = time.Second * 5
		output, err := cmd.Output()
		switch {
		case cmdCtx.Err() == context.DeadlineExceeded:
			result.Data = fmt.Sprintf("任务执行超时\n%s", string(output))
		case err != nil:
			result.Data += fmt.Sprintf("%s\n%s", string(output), err.Error())
		default:
			result.Data = string(output)
			result.Successful = true
		}
		result.Delay = float32(time.Now().Sub(startedAt).Seconds())
	default:
		log.Printf("Unknown action: %v", task)
	}
	reportTaskResult(client, &result)
}

// httpTaskOutcome 组装 HTTP 监控任务的上报结果：Data 与是否成功。
//
// 证书数据（"Issuer|NotAfter" 或 "SSL证书错误：..."）优先作为 Data，
// 服务端据此判断证书到期与变更；传输层失败时退回错误信息，此时不查证书。
// Successful 只由 HTTP 状态决定，证书检查结果不改变它——站点返回 4xx/5xx
// 仍算不可用，但证书到期与否是另一回事，两者互不掩盖。
func httpTaskOutcome(reqErr, statusErr error, certData string) (string, bool) {
	if reqErr != nil {
		return reqErr.Error(), false
	}
	if certData != "" {
		return certData, statusErr == nil
	}
	if statusErr != nil {
		return statusErr.Error(), false
	}
	return "", true
}

// fetchCertData 检查 HTTPS 目标的证书，返回上报用的 Data 字段。
// 非 HTTPS 目标返回空串，由调用方回落到 HTTP 错误信息；
// 取证书失败返回「SSL证书错误：...」，成功返回 "Issuer|NotAfter"。
func fetchCertData(taskData string) string {
	hostport, isHTTPS, err := parseCertTarget(taskData)
	if err != nil {
		return "SSL证书错误：" + err.Error()
	}
	if !isHTTPS {
		return ""
	}
	c := cert.NewCert(hostport)
	if c.Error != "" {
		return "SSL证书错误：" + c.Error
	}
	return c.Issuer + "|" + c.NotAfter
}

func parseCertTarget(rawURL string) (hostport string, isHTTPS bool, err error) {
	rawURL = strings.TrimSpace(rawURL)
	u, parseErr := url.Parse(rawURL)
	if parseErr == nil && u.Scheme != "" {
		if !strings.EqualFold(u.Scheme, "https") {
			return "", false, nil
		}
		host := strings.ToLower(u.Hostname())
		if host == "" {
			return "", true, errors.New("empty host in url")
		}
		port := u.Port()
		if port == "" {
			port = "443"
		}
		return net.JoinHostPort(host, port), true, nil
	}

	lower := strings.ToLower(rawURL)
	if strings.HasPrefix(lower, "https://") {
		target := strings.TrimSpace(lower[8:])
		if idx := strings.IndexAny(target, "/?#"); idx != -1 {
			target = target[:idx]
		}
		if target == "" {
			return "", true, errors.New("empty host in url")
		}
		if !strings.Contains(target, ":") {
			target = net.JoinHostPort(target, "443")
		}
		return target, true, nil
	}

	return "", false, nil
}

func reportTaskResult(client pb.ProbeServiceClient, result *pb.TaskResult) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
	defer cancel()
	if _, err := client.ReportTask(ctx, result); err != nil {
		log.Printf("client.ReportTask err: %v", err)
	}
}

func reportSystemInfo(parent context.Context, client pb.ProbeServiceClient) error {
	host := monitor.GetHost().PB()
	ctx, cancel := context.WithTimeout(parent, time.Second*30)
	defer cancel()
	_, err := client.ReportSystemInfo(ctx, host)
	return err
}

func reportSystemState(parent context.Context, client pb.ProbeServiceClient) error {
	state := monitor.GetState(dao.ReportDelay).PB()
	ctx, cancel := context.WithTimeout(parent, time.Second*30)
	defer cancel()
	_, err := client.ReportSystemState(ctx, state)
	return err
}

func reportState(ctx context.Context, client pb.ProbeServiceClient) error {
	var lastReportHostInfo time.Time
	var err error
	defer log.Printf("reportState exit %v => %v", time.Now(), err)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		monitor.TrackNetworkSpeed()
		if err = reportSystemState(ctx, client); err != nil {
			return err
		}
		if lastReportHostInfo.Before(time.Now().Add(-10 * time.Minute)) {
			lastReportHostInfo = time.Now()
			if err = reportSystemInfo(ctx, client); err != nil {
				return err
			}
		}
	}
}
