/*
Copyright 2026.

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

package controller

import (
	stderrors "errors"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/shamubernetes/n8n-operator/pkg/n8n"
)

const cleanupFinalizerMaxRetry = 10 * time.Minute

func isN8NNotFound(err error) bool {
	apiErr := &n8n.APIError{}
	if !stderrors.As(err, &apiErr) {
		return false
	}
	return apiErr.Code == 404
}

func shouldForceFinalize(deletionTimestamp *metav1.Time, err error) bool {
	if err == nil {
		return false
	}

	if apierrors.IsNotFound(err) || isN8NNotFound(err) {
		return true
	}

	if deletionTimestamp == nil {
		return false
	}

	return time.Since(deletionTimestamp.Time) >= cleanupFinalizerMaxRetry
}
