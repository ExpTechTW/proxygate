package web

import (
	"embed"
	"io/fs"

	"github.com/gofiber/fiber/v3"
	staticmiddleware "github.com/gofiber/fiber/v3/middleware/static"
)

//go:embed dist/*
var frontend embed.FS

func Handler() fiber.Handler {
	directory, err := fs.Sub(frontend, "dist")
	if err != nil {
		panic(err)
	}
	index, err := fs.ReadFile(directory, "index.html")
	if err != nil {
		panic(err)
	}
	return staticmiddleware.New(".", staticmiddleware.Config{
		FS: directory,
		NotFoundHandler: func(c fiber.Ctx) error {
			c.Status(fiber.StatusOK)
			c.Type("html")
			return c.Send(index)
		},
	})
}
