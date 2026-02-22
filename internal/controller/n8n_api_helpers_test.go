package controller

import (
	"errors"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/shamubernetes/n8n-operator/pkg/n8n"
)

func TestShouldForceFinalize(t *testing.T) {
	oldDeletion := metav1.NewTime(time.Now().Add(-(cleanupFinalizerMaxRetry + time.Minute)))
	recentDeletion := metav1.NewTime(time.Now())

	tests := []struct {
		name              string
		deletionTimestamp *metav1.Time
		err               error
		want              bool
	}{
		{
			name: "nil error does not force",
			want: false,
		},
		{
			name: "kubernetes not found forces",
			err:  apierrors.NewNotFound(schema.GroupResource{Group: "", Resource: "secrets"}, "missing"),
			want: true,
		},
		{
			name: "n8n 404 forces",
			err:  &n8n.APIError{Code: 404, Message: "not found"},
			want: true,
		},
		{
			name:              "generic error forces after timeout",
			deletionTimestamp: &oldDeletion,
			err:               errors.New("transient failure"),
			want:              true,
		},
		{
			name:              "generic error does not force before timeout",
			deletionTimestamp: &recentDeletion,
			err:               errors.New("transient failure"),
			want:              false,
		},
		{
			name: "generic error without deletion timestamp does not force",
			err:  errors.New("transient failure"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldForceFinalize(tt.deletionTimestamp, tt.err)
			if got != tt.want {
				t.Fatalf("shouldForceFinalize() = %v, want %v", got, tt.want)
			}
		})
	}
}
