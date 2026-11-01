import { test, expect } from 'bun:test';
import { routeMeta, navSections, routeTitles } from './routeMeta.js';

test('paths are unique and titled', () => {
  const paths = routeMeta.map((r) => r.path);
  expect(new Set(paths).size).toBe(paths.length);
  for (const r of routeMeta) {
    expect(routeTitles[r.path]).toBe(r.title);
    expect(r.icon).toBeTruthy();
  }
});

test('every route belongs to exactly one nav section', () => {
  const seen = [];
  for (const section of navSections) {
    for (const item of section.items) seen.push(item.path);
  }
  expect(seen.sort()).toEqual(routeMeta.map((r) => r.path).sort());
});

test('nav adminOnly mirrors route metadata', () => {
  const byPath = Object.fromEntries(routeMeta.map((r) => [r.path, !!r.adminOnly]));
  for (const section of navSections) {
    for (const item of section.items) {
      expect(!!item.adminOnly).toBe(byPath[item.path]);
    }
  }
});
