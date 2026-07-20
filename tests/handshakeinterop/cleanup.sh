#!/usr/bin/env sh

remove_owned_temp() {
  if [ "$#" -ne 2 ]; then
    echo "remove_owned_temp requires a root and base" >&2
    return 2
  fi

  _interop_owned_root="$1"
  _interop_owned_base="$2"
  _interop_root_leaf="$(basename -- "$_interop_owned_root")"
  _interop_root_suffix="${_interop_root_leaf#cap-handshake-interop.}"
  if [ "$(dirname -- "$_interop_owned_root")" != "$_interop_owned_base" ] ||
    [ "$_interop_root_suffix" = "$_interop_root_leaf" ] ||
    [ "${#_interop_root_suffix}" -lt 6 ] || [ -L "$_interop_owned_root" ]; then
    echo "refusing unsafe cleanup path: $_interop_owned_root" >&2
    return 1
  fi
  case "$_interop_root_suffix" in
    *[![:alnum:]]*)
      echo "refusing unsafe cleanup path: $_interop_owned_root" >&2
      return 1
      ;;
  esac
  [ -e "$_interop_owned_root" ] || return 0

  _interop_module_cache="$_interop_owned_root/go-consumer/.gomodcache"
  if [ -L "$_interop_module_cache" ]; then
    echo "refusing symlinked module cache: $_interop_module_cache" >&2
    return 1
  fi
  if [ -d "$_interop_module_cache" ] && ! chmod -R u+w -- "$_interop_module_cache"; then
    echo "could not restore module-cache permissions: $_interop_module_cache" >&2
  fi
  rm -rf -- "$_interop_owned_root"
}
