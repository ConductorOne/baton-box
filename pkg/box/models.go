package box

type BaseType struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

type User struct {
	BaseType
	Enterprise Enterprise `json:"enterprise"`
	Login      string     `json:"login"`
	Name       string     `json:"name"`
	Phone      string     `json:"phone"`
	Role       string     `json:"role"`
	Status     string     `json:"status"`
}

type Enterprise struct {
	BaseType
	Name string `json:"name"`
}

type Group struct {
	BaseType
	InvitabilityLevel      string `json:"invitability_level"`
	MemberViewabilityLevel string `json:"member_viewability_level"`
	Name                   string `json:"name"`
}

type GroupMembership struct {
	BaseType
	Role  string `json:"role"`
	User  User   `json:"user"`
	Group Group  `json:"group"`
}
