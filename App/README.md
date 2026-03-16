dependency tree

main : assignerthread, networkthread, elevatorthread
  networkthread : network, worldview
  elevatorthread : elevator, synchronizer
    elevator : requests

run ./count_app_go_lines.sh