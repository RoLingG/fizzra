/**
 * 给展示用的代码片段做轻量语法着色
 *
 * 只做词法层面的切分（注释、字符串、数字、关键字），不解析语法树，也不引入高亮库：
 * 片段只有十几行，目的是让重复代码块和修复示例看起来像代码而不是纯文本。配色来自
 * 设计 token，深浅主题自动跟随。
 */

/** 着色后的片段：每段是 { text, cls }，cls 为空表示普通文本 */
export interface CodeToken {
  text: string
  cls: '' | 'cm' | 'st' | 'kw' | 'nu' | 'fn'
}

type Lang = 'go' | 'javascript' | 'python' | 'rust' | 'java' | 'csharp' | 'cpp' | 'php' | 'generic'

const KEYWORDS: Record<Lang, string[]> = {
  go: [
    'break', 'case', 'chan', 'const', 'continue', 'default', 'defer', 'else', 'fallthrough',
    'for', 'func', 'go', 'goto', 'if', 'import', 'interface', 'map', 'package', 'range',
    'return', 'select', 'struct', 'switch', 'type', 'var',
  ],
  javascript: [
    'async', 'await', 'break', 'case', 'catch', 'class', 'const', 'continue', 'default',
    'delete', 'do', 'else', 'export', 'extends', 'finally', 'for', 'function', 'if',
    'import', 'in', 'instanceof', 'let', 'new', 'of', 'return', 'static', 'super',
    'switch', 'this', 'throw', 'try', 'typeof', 'var', 'void', 'while', 'yield',
  ],
  python: [
    'and', 'as', 'assert', 'async', 'await', 'break', 'class', 'continue', 'def', 'del',
    'elif', 'else', 'except', 'finally', 'for', 'from', 'global', 'if', 'import', 'in',
    'is', 'lambda', 'nonlocal', 'not', 'or', 'pass', 'raise', 'return', 'try', 'while',
    'with', 'yield',
  ],
  rust: [
    'as', 'async', 'await', 'break', 'const', 'continue', 'crate', 'dyn', 'else', 'enum',
    'fn', 'for', 'if', 'impl', 'in', 'let', 'loop', 'match', 'mod', 'move', 'mut', 'pub',
    'ref', 'return', 'self', 'struct', 'trait', 'type', 'unsafe', 'use', 'where', 'while',
  ],
  java: [
    'abstract', 'assert', 'boolean', 'break', 'case', 'catch', 'class', 'continue',
    'default', 'do', 'else', 'enum', 'extends', 'final', 'finally', 'for', 'if',
    'implements', 'import', 'instanceof', 'interface', 'new', 'package', 'private',
    'protected', 'public', 'return', 'static', 'super', 'switch', 'throw', 'throws',
    'try', 'void', 'while',
  ],
  csharp: [
    'abstract', 'as', 'async', 'await', 'bool', 'break', 'case', 'catch', 'class', 'const',
    'continue', 'default', 'do', 'else', 'enum', 'finally', 'for', 'foreach', 'if', 'in',
    'internal', 'namespace', 'new', 'private', 'protected', 'public', 'return', 'static',
    'struct', 'switch', 'throw', 'try', 'using', 'var', 'virtual', 'void', 'while',
  ],
  cpp: [
    'auto', 'bool', 'break', 'case', 'catch', 'class', 'const', 'constexpr', 'continue',
    'default', 'delete', 'do', 'else', 'enum', 'explicit', 'for', 'if', 'inline',
    'namespace', 'new', 'nullptr', 'private', 'protected', 'public', 'return', 'sizeof',
    'static', 'struct', 'switch', 'template', 'throw', 'try', 'typedef', 'typename',
    'using', 'virtual', 'void', 'while',
  ],
  php: [
    'abstract', 'and', 'array', 'as', 'break', 'case', 'catch', 'class', 'const',
    'continue', 'default', 'do', 'echo', 'else', 'elseif', 'extends', 'final', 'finally',
    'for', 'foreach', 'function', 'if', 'implements', 'interface', 'namespace', 'new',
    'private', 'protected', 'public', 'return', 'static', 'switch', 'throw', 'try',
    'use', 'while',
  ],
  generic: [],
}

/** 行注释与块注释的起始符，按语言取 */
const COMMENT_MARKS: Record<Lang, { line: string[]; block?: [string, string] }> = {
  go: { line: ['//'], block: ['/*', '*/'] },
  javascript: { line: ['//'], block: ['/*', '*/'] },
  python: { line: ['#'] },
  rust: { line: ['//'], block: ['/*', '*/'] },
  java: { line: ['//'], block: ['/*', '*/'] },
  csharp: { line: ['//'], block: ['/*', '*/'] },
  cpp: { line: ['//'], block: ['/*', '*/'] },
  php: { line: ['//', '#'], block: ['/*', '*/'] },
  generic: { line: ['//'], block: ['/*', '*/'] },
}

/** 把文件扩展名映射到语言标识 */
export function languageOf(path: string | undefined): Lang {
  const ext = (path ?? '').split('.').pop()?.toLowerCase() ?? ''
  switch (ext) {
    case 'go':
      return 'go'
    case 'js':
    case 'jsx':
    case 'ts':
    case 'tsx':
    case 'mjs':
    case 'cjs':
      return 'javascript'
    case 'py':
      return 'python'
    case 'rs':
      return 'rust'
    case 'java':
      return 'java'
    case 'cs':
      return 'csharp'
    case 'c':
    case 'cc':
    case 'cpp':
    case 'cxx':
    case 'h':
    case 'hpp':
      return 'cpp'
    case 'php':
      return 'php'
    default:
      return 'generic'
  }
}

function push(tokens: CodeToken[], text: string, cls: CodeToken['cls']) {
  if (text === '') {
    return
  }
  const last = tokens[tokens.length - 1]
  if (last && last.cls === cls) {
    last.text += text
    return
  }
  tokens.push({ text, cls })
}

/** 判断某个位置是否是标识符字符 */
function isIdent(ch: string): boolean {
  return /[A-Za-z0-9_$]/.test(ch)
}

/**
 * 把一段源码切成着色片段
 *
 * 处理顺序是注释、字符串、数字、标识符，其余字符原样累积；跨行的块注释和字符串
 * 用 inBlock / inString 在行间传递状态
 */
export function tokenizeCode(src: string, lang: Lang): CodeToken[] {
  const tokens: CodeToken[] = []
  const keywords = new Set(KEYWORDS[lang])
  const marks = COMMENT_MARKS[lang]
  let inBlock: string | null = null

  for (const line of src.split('\n')) {
    let i = 0
    let plain = ''

    while (i < line.length) {
      if (inBlock) {
        const end = line.indexOf(inBlock, i)
        if (end < 0) {
          push(tokens, line.slice(i), 'cm')
          plain = ''
          i = line.length
          break
        }
        push(tokens, plain, '')
        plain = ''
        push(tokens, line.slice(i, end + inBlock.length), 'cm')
        i = end + inBlock.length
        inBlock = null
        continue
      }

      const rest = line.slice(i)

      // 注释
      const lineMark = marks.line.find((m) => rest.startsWith(m))
      if (lineMark) {
        push(tokens, plain, '')
        plain = ''
        push(tokens, rest, 'cm')
        i = line.length
        break
      }
      if (marks.block && rest.startsWith(marks.block[0]) && !inStringAt(line, i)) {
        const [open, close] = marks.block
        const end = line.indexOf(close, i + open.length)
        push(tokens, plain, '')
        plain = ''
        if (end < 0) {
          push(tokens, line.slice(i), 'cm')
          inBlock = close
          i = line.length
          break
        }
        push(tokens, line.slice(i, end + close.length), 'cm')
        i = end + close.length
        continue
      }

      // 字符串
      const ch = line[i]
      if (ch === '"' || ch === "'" || ch === '`') {
        push(tokens, plain, '')
        plain = ''
        let j = i + 1
        while (j < line.length) {
          if (line[j] === '\\') {
            j += 2
            continue
          }
          if (line[j] === ch) {
            j++
            break
          }
          j++
        }
        push(tokens, line.slice(i, j), 'st')
        i = j
        continue
      }

      // 数字
      if (/[0-9]/.test(ch) && (i === 0 || !isIdent(line[i - 1]))) {
        const m = /^[0-9][0-9a-fA-FxX._]*/.exec(rest)
        if (m) {
          push(tokens, plain, '')
          plain = ''
          push(tokens, m[0], 'nu')
          i += m[0].length
          continue
        }
      }

      // 标识符与关键字
      if (isIdent(ch)) {
        const m = /^[A-Za-z0-9_$]+/.exec(rest)
        if (m) {
          const word = m[0]
          push(tokens, plain, '')
          plain = ''
          if (keywords.has(word)) {
            push(tokens, word, 'kw')
          } else if (line[i + word.length] === '(') {
            push(tokens, word, 'fn')
          } else {
            push(tokens, word, '')
          }
          i += word.length
          continue
        }
      }

      plain += ch
      i++
    }

    push(tokens, plain, '')
    push(tokens, '\n', '')
  }

  return tokens
}

/** 粗略判断某位置是否落在字符串内，用于避免把字符串里的 /* 当成块注释 */
function inStringAt(line: string, pos: number): boolean {
  let quote = ''
  let escaped = false
  for (let i = 0; i < pos; i++) {
    const c = line[i]
    if (escaped) {
      escaped = false
      continue
    }
    if (c === '\\') {
      escaped = true
      continue
    }
    if (quote) {
      if (c === quote) {
        quote = ''
      }
      continue
    }
    if (c === '"' || c === "'" || c === '`') {
      quote = c
    }
  }
  return quote !== ''
}
