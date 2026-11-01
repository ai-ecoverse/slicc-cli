package flow

type Resources struct {
	Browsers       []ResourceBrowser `json:"browsers"`
	Mounts         []ResourceMount   `json:"mounts"`
	RejectedMounts []RejectedMount   `json:"rejectedMounts"`
	Network        ResourceNetwork   `json:"network"`
	Compute        ResourceCompute   `json:"compute"`
	Jerk           ResourceJerk      `json:"jerk"`
	KernelPort     int               `json:"kernelPort,omitempty"`
}

type ResourceBrowser struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Role  string `json:"role"`
	Owned bool   `json:"owned"`
}

type ResourceMount struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	ReadOnly bool   `json:"readOnly"`
}

type RejectedMount struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type ResourceNetwork struct {
	Exit   bool   `json:"exit"`
	Policy string `json:"policy"`
}

type ResourceCompute struct {
	SSH  bool `json:"ssh"`
	SFTP bool `json:"sftp"`
}

type ResourceJerk struct {
	Offered bool `json:"offered"`
	Seal    bool `json:"seal"`
}
