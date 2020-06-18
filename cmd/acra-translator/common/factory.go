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

package common

import (
	"google.golang.org/grpc"
)

// GRPCServerFactory factory which return new generated grpc.Server which implements API
type GRPCServerFactory interface {
	New(data *TranslatorData, opts ...grpc.ServerOption) (*grpc.Server, error)
}