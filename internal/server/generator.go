package server

type robin struct {
	e []string
	i int
}

func (g *robin) next() string {
	v := g.e[g.i]
	g.i += 1
	if g.i > len(g.e)-1 {
		g.i = 0
	}
	return v
}
