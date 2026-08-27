package bench

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Workload describes a YCSB CoreWorkload-compatible operation mix.
type Workload struct {
	Name                string  `yaml:"name"`
	RecordCount         int     `yaml:"record_count"`
	OperationCount      int     `yaml:"operation_count"`
	ReadProportion      float64 `yaml:"read_proportion"`
	UpdateProportion    float64 `yaml:"update_proportion"`
	InsertProportion    float64 `yaml:"insert_proportion"`
	KeyPrefix           string  `yaml:"key_prefix"`
	FieldLength         int     `yaml:"field_length"`
	RequestDistribution string  `yaml:"request_distribution"`
	ZipfianConstant     float64 `yaml:"zipfian_constant"`
}

func LoadWorkload(path string) (Workload, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Workload{}, err
	}

	var w Workload
	if err := yaml.Unmarshal(data, &w); err != nil {
		return Workload{}, err
	}
	if w.KeyPrefix == "" {
		w.KeyPrefix = "user"
	}
	if w.RequestDistribution == "zipfian" && w.ZipfianConstant == 0 {
		w.ZipfianConstant = 0.99
	}
	return w, w.Validate()
}

func (w Workload) Validate() error {
	if w.RecordCount <= 0 {
		return fmt.Errorf("record_count must be > 0")
	}
	if w.OperationCount <= 0 {
		return fmt.Errorf("operation_count must be > 0")
	}
	if w.KeyPrefix == "" {
		w.KeyPrefix = "user"
	}
	sum := w.ReadProportion + w.UpdateProportion + w.InsertProportion
	if sum < 0.99 || sum > 1.01 {
		return fmt.Errorf("operation proportions must sum to 1.0, got %.2f", sum)
	}
	if w.FieldLength <= 0 {
		return fmt.Errorf("field_length must be > 0")
	}
	if w.RequestDistribution == "" {
		return fmt.Errorf("request_distribution is required")
	}
	switch w.RequestDistribution {
	case "uniform", "zipfian":
	default:
		return fmt.Errorf("unsupported request_distribution %q (use uniform or zipfian)", w.RequestDistribution)
	}
	return nil
}

func (w Workload) KeyFor(i int) string {
	return fmt.Sprintf("%s%d", w.KeyPrefix, i)
}
