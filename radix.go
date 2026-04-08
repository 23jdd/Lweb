package main

import (
	"fmt"
	"strings"
)

type TrieNode struct {
	Next   []*TrieNode
	index  map[string]*TrieNode
	prefix string
	IsEnd  bool //
	IsPath bool // {path}
	IsAll  bool // *arg
	IsRoot     bool
	Path       string
	Middleware []Handler //
	Solve      Handler
}

func NewTrieNode(prefix string) *TrieNode {
	return &TrieNode{
		prefix: prefix,
		index:  make(map[string]*TrieNode),
	}
}

type Trie struct {
	root *TrieNode
}

func (t *Trie) Insert(value []string, handler Handler, Used bool) {
	temp := t.root
	for index, v := range value {
		IsPath := false
		IsAll := false
		if strings.HasPrefix(v, "{") && strings.HasSuffix(v, "}") {
			v = v[1 : len(v)-1]
			IsPath = true
		} else if strings.HasPrefix(v, "*") {
			if len(v) == 1 {
				panic("wildcard name is required")
			}
			if index != len(value)-1 {
				panic("wildcard must be the final segment")
			}
			v = v[1:]
			IsAll = true
		}
		if len(v) == 0 {
			panic("Empty string")
		}
		if !Used {
			for _, child := range temp.Next {
				// 同层参数节点只能有一个，避免 /{id} 与 /{name} 歧义
				if IsPath && child.IsPath && child.prefix != v {
					panic(fmt.Sprintf("path param conflict: {%s} vs {%s}", child.prefix, v))
				}
				// 通配节点必须独占同层且必须最后匹配，避免与静态段冲突
				if IsAll && child.prefix != v {
					panic(fmt.Sprintf("wildcard conflict with sibling segment: *%s", v))
				}
				if !IsAll && child.IsAll {
					panic(fmt.Sprintf("segment '%s' conflicts with wildcard '*%s'", v, child.prefix))
				}
			}
		}
		node, ok := temp.index[v]
		if !ok {
			//
			node = NewTrieNode(v)
			if IsPath {
				node.Path = v
				node.IsPath = true
			}
			if IsAll {
				node.Path = v
				node.IsAll = true
			}
			temp.Next = append(temp.Next, node)
			temp.index[v] = node
			temp = node
		} else {
			if node.IsEnd && index == len(value)-1 {
				if Used {
					node.Middleware = append(node.Middleware, handler)
					return
				} else {
					panic("重复") //
				}

			}
			temp = node
		}
		if index == len(value)-1 {
			if Used {
				temp.Middleware = append(temp.Middleware, handler)
				return
			}
			temp.IsEnd = true
			temp.Solve = handler
		}
	}
}
func (t *Trie) Search(value []string) ([]Handler, map[string]string, error) {
	result := make([]Handler, 0, len(value)+2)
	params := make(map[string]string)
	temp := t.root
	for index, v := range value {
		node, ok := temp.index[v]
		if !ok {
			// 没有精确匹配时，尝试参数路由节点
			for _, child := range temp.Next {
				if child.IsPath {
					node = child
					ok = true
					break
				}
			}
		}
		if !ok {
			// 参数节点也不匹配时，尝试通配节点 *arg
			for _, child := range temp.Next {
				if child.IsAll {
					node = child
					ok = true
					params[child.Path] = strings.Join(value[index:], "/")
					result = append(result, child.Middleware...)
					if child.IsEnd {
						result = append(result, child.Solve)
						return result, params, nil
					}
					return nil, nil, fmt.Errorf("invalid wildcard route %s", child.Path)
				}
			}
			if !ok {
				return nil, nil, fmt.Errorf("not found %s", v)
			}
		}
		if node.IsPath {
			params[node.Path] = v
		}
		if node.IsAll {
			params[node.Path] = strings.Join(value[index:], "/")
			result = append(result, node.Middleware...)
			if node.IsEnd {
				result = append(result, node.Solve)
				return result, params, nil
			}
			return nil, nil, fmt.Errorf("invalid wildcard route %s", node.Path)
		}
		result = append(result, node.Middleware...)
		if node.IsEnd && index == len(value)-1 {
			result := append(result, node.Solve)
			return result, params, nil
		}
		temp = node
	}
	return result, params, nil
}
func (t *Trie) Display() {
	fmt.Println("Trie Tree:")
	t.displayNode(t.root, "", true, "")
}

// displayNode 递归打印节点
func (t *Trie) displayNode(node *TrieNode, prefix string, isLast bool, indent string) {
	// 根节点不打印自身
	if node != t.root {
		connector := "├── "
		if isLast {
			connector = "└── "
		}

		endMark := ""
		if node.IsEnd {
			endMark = " [END]"
		}
		fmt.Printf("%s%s'%s'%s\n", indent, connector, node.prefix, endMark)
	}

	// 获取所有子节点（需要稳定顺序以保证输出一致）
	children := node.Next
	for i, child := range children {
		isLastChild := i == len(children)-1
		// 构建新的缩进
		newIndent := indent
		if node != t.root {
			if isLast {
				newIndent += "    "
			} else {
				newIndent += "│   "
			}
		}
		t.displayNode(child, prefix+child.prefix, isLastChild, newIndent)
	}
}
func NewRouterTree() *Trie {
	t := &Trie{root: NewTrieNode("")}
	for _, method := range routeMethods {
		node := NewTrieNode(method)
		t.root.Next = append(t.root.Next, node)
		t.root.index[method] = node
	}
	return t
}

