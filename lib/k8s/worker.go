package k8s

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type (
	Action       int
	PortResponse struct {
		Port      int
		Error     error
		LobbyCode string
	}
	Request struct {
		Action    Action
		LobbyCode string
		PortResp  chan PortResponse
	}
	gameserverData struct {
		Identifier string
		Port       int
	}
)

const (
	GetFreeAction Action = iota + 1
	GetLobbyAction
	MarkRunningAction

	MinFreeServers  int = 1
	MaxServers      int = 8
	LobbyCodeLength int = 5
)

func Worker() chan<- Request {
	config, err := rest.InClusterConfig()
	if err != nil {
		panic(err.Error())
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	freeListLock := sync.Mutex{}
	freeList := make([]gameserverData, 0)

	requestChan := make(chan Request)
	go func() {
		for {
			r := <-requestChan
			switch r.Action {
			case GetFreeAction:
				if len(freeList) <= 0 {
					responseWithTimeout(r.PortResp, PortResponse{Error: errors.New("no server available rn")})
					continue
				}

				freeListLock.Lock()
				gsData := freeList[0]
				freeList = freeList[1:]
				freeListLock.Unlock()

				responseWithTimeout(r.PortResp, PortResponse{Port: gsData.Port, LobbyCode: gsData.Identifier})

			case GetLobbyAction:
				code := r.LobbyCode
				if !validCode(code) {
					logrus.Warn("invalid code submitted")
					responseWithTimeout(r.PortResp, PortResponse{Error: fmt.Errorf("invalid code")})
					continue
				}

				services, err := clientset.CoreV1().Services("teamfactory").List(context.Background(), metav1.ListOptions{
					LabelSelector: fmt.Sprintf("game=teamfactory,ident=%s", code),
				})
				if err != nil {
					logrus.WithError(err).Error("error listing services")
					responseWithTimeout(r.PortResp, PortResponse{Error: fmt.Errorf("internal error")})
					continue
				}

				for _, s := range services.Items {
					responseWithTimeout(r.PortResp, PortResponse{
						Port: int(s.Spec.Ports[0].NodePort),
					})
					continue
				}

				logrus.Warn("no matching service found")
				responseWithTimeout(r.PortResp, PortResponse{Error: fmt.Errorf("not found")})
			}
		}
	}()

	go func() {
		for {
			time.Sleep(time.Second * 5)
			if len(freeList) >= MinFreeServers {
				continue
			}

			ident := String(LobbyCodeLength)
			_, err := clientset.CoreV1().Pods("teamfactory").Create(context.Background(), &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "teamfactory-gameserver",
					Labels: map[string]string{
						"game":  "teamfactory",
						"ident": ident,
					},
				},
				Spec: v1.PodSpec{
					RestartPolicy: v1.RestartPolicyNever,
					ImagePullSecrets: []v1.LocalObjectReference{
						{Name: "dockerconfigjson-ghcr"},
					},
					Containers: []v1.Container{
						{
							ImagePullPolicy: v1.PullAlways,
							TTY:             true,
							Name:            "gameserver",
							Image:           "ghcr.io/devnull-twitch/teamfactory-server:latest",
							Ports: []v1.ContainerPort{
								{ContainerPort: 50125, Protocol: v1.ProtocolUDP},
							},
						},
					},
				},
			}, metav1.CreateOptions{})

			if err != nil {
				panic(fmt.Errorf("pod creation panic: %w", err))
			}

			newService, err := clientset.CoreV1().Services("teamfactory").Create(context.Background(), &v1.Service{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "teamfactory-gs-service",
					Labels: map[string]string{
						"game":  "teamfactory",
						"ident": ident,
					},
				},
				Spec: v1.ServiceSpec{
					Selector: map[string]string{
						"game":  "teamfactory",
						"ident": ident,
					},
					Type: v1.ServiceTypeNodePort,
					Ports: []v1.ServicePort{
						{Port: 50125, Protocol: v1.ProtocolUDP},
					},
				},
			}, metav1.CreateOptions{})

			if err != nil {
				panic(fmt.Errorf("service creation panic: %w", err))
			}

			freeListLock.Lock()
			freeList = append(freeList, gameserverData{
				Port:       int(newService.Spec.Ports[0].NodePort),
				Identifier: ident,
			})
			freeListLock.Unlock()
		}
	}()

	return requestChan
}

func responseWithTimeout(channel chan<- PortResponse, resp PortResponse) {
	select {
	case channel <- resp:
	case <-time.After(time.Second):
	}
}

func validCode(code string) bool {
	if len(code) != LobbyCodeLength {
		return false
	}
	codeReader := strings.NewReader(code)
	for {
		codeRune, _, err := codeReader.ReadRune()
		if err == io.EOF {
			return true
		}
		if !strings.ContainsRune(charset, codeRune) {
			return false
		}
	}
}
