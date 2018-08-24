package discover

type Table struct {
	self *Node
}

func (tab *Table) Self() *Node {
	return tab.self
}

func (t *Table) Close() {
}

func (t *Table) Resolve(target NodeID) *Node {
	return nil
}

func (t *Table) Lookup(target NodeID) []*Node {
	return nil
}

func (t *Table) ReadRandomNodes([]*Node) int {
	return 0
}
