/*
Copyright 2020, Cossack Labs Limited

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package grpc_api

import (
	"github.com/cossacklabs/acra/cmd/acra-translator/common"
	"github.com/cossacklabs/acra/logging"
	"github.com/cossacklabs/acra/network"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

// GRPCServerFactory return factory which generate new grpc.Server with Translator gRPC API implementation
type GRPCServerFactory struct{}

// New return new generated grpc.Server with gRPC Translator API
func (factory *GRPCServerFactory) New(data *common.TranslatorData) (*grpc.Server, error) {
	grpcServer := grpc.NewServer(grpc.ConnectionTimeout(network.DefaultNetworkTimeout))
	service, err := NewDecryptGRPCService(data)
	if err != nil {
		logrus.WithError(err).WithField(logging.FieldKeyEventCode, logging.EventCodeErrorTranslatorCantHandleGRPCConnection).
			Errorln("Can't create grpc service")
		return nil, err
	}
	RegisterReaderServer(grpcServer, service)
	RegisterWriterServer(grpcServer, service)
	// Register reflection service on gRPC server.
	reflection.Register(grpcServer)
	return grpcServer, nil
}