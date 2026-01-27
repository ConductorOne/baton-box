package main

import (
	cfg "github.com/conductorone/baton-box/pkg/config"
	"github.com/conductorone/baton-sdk/pkg/config"
)

func main() {
	config.Generate("box", cfg.Config)
}
