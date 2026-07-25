"""LangGraph integration for LadyM — two paths to use ladyM as a memory layer.

* Path A (Tools): ``create_ladym_tools`` -> LangChain BaseTools for ReAct agents.
* Path B (Nodes): ``create_recall_node`` / ``create_retain_node`` -> graph nodes
  for automatic per-turn memory injection.

Install: ``pip install 'ladym[langgraph]'``.
"""
from __future__ import annotations

from .nodes import create_recall_node, create_retain_node
from .tools import create_ladym_tools

__all__ = [
    "create_ladym_tools",
    "create_recall_node",
    "create_retain_node",
]
