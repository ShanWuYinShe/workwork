// @ts-check
/**
 * 日志搜索过滤(纯函数,单测锁定回归):元素是 {id,text,level} 对象,
 * 此前模板内联实现对对象调 toLowerCase,搜索框一输入即 TypeError
 * 导致日志页渲染挂死(PR #16 修复)。
 * @param {{ id: number, text: string, level: string }[]} lines
 * @param {string} search 空串返回原数组(与修复后行为一致)
 * @returns {{ id: number, text: string, level: string }[]}
 */
export function filterLogLines(lines, search) {
  if (!search) return lines;
  const q = search.toLowerCase();
  return lines.filter((l) => l.text.toLowerCase().includes(q));
}
