// Copyright 2023 Greptime Team
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dbconfig

import (
	"github.com/GreptimeTeam/greptimedb-operator/apis/v1alpha1"
)

var _ Config = &FrontendConfig{}

// FrontendConfig is the configuration for the frontend.
type FrontendConfig struct {
	// LoggingConfig is the configuration for the logging.
	LoggingConfig `tomlmapping:",inline"`

	// InputConfig is from config field of cluster spec.
	InputConfig string
}

// ConfigureByFrontend configures the frontend configuration by the given cluster.
func (c *FrontendConfig) ConfigureByFrontend(frontend *v1alpha1.FrontendSpec) error {
	if cfg := frontend.GetConfig(); cfg != "" {
		if err := c.SetInputConfig(cfg); err != nil {
			return err
		}
	}

	c.ConfigureLogging(frontend.GetLogging())

	return nil
}

// ConfigureByCluster is not need to implement in frontend components.
func (c *FrontendConfig) ConfigureByCluster(_ *v1alpha1.GreptimeDBCluster) error {
	return nil
}

// ConfigureByStandalone is not need to implement in cluster mode.
func (c *FrontendConfig) ConfigureByStandalone(_ *v1alpha1.GreptimeDBStandalone) error {
	return nil
}

// Kind returns the component kind of the frontend.
func (c *FrontendConfig) Kind() v1alpha1.ComponentKind {
	return v1alpha1.FrontendComponentKind
}

// GetInputConfig returns the input config of the frontend.
func (c *FrontendConfig) GetInputConfig() string {
	return c.InputConfig
}

// SetInputConfig sets the input config of the frontend.
func (c *FrontendConfig) SetInputConfig(inputConfig string) error {
	c.InputConfig = inputConfig
	return nil
}
